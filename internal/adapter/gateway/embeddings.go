package gateway

import (
	"context"
	"fmt"
	"memd/internal/domain"
)

const (
	// embeddingsPath is the OpenAI embeddings route under BaseURL.
	embeddingsPath = "/embeddings"
	// documentPrefix is nomic's task prefix for stored text.
	documentPrefix = "search_document: "
	// queryPrefix is nomic's task prefix for search queries.
	queryPrefix = "search_query: "
)

// embeddingsResponse is the subset of an embeddings response the client reads.
type embeddingsResponse struct {
	// Model is the model that served the call.
	Model string `json:"model"`
	// Data holds one embedding per input, each tagged with its input index.
	Data []struct {
		// Index is the position of the input this embedding belongs to.
		Index int `json:"index"`
		// Embedding is the vector.
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	// Usage is the token usage.
	Usage *apiUsage `json:"usage"`
}

// EmbedDocuments embeds stored text with nomic's "search_document: " prefix, EmbedBatchSize texts per
// request, and returns one domain.EmbeddingDim vector per input, in order.
func (c *Client) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += c.cfg.EmbedBatchSize {
		batch := texts[start:min(start+c.cfg.EmbedBatchSize, len(texts))]
		vecs, err := c.embed(ctx, withPrefix(documentPrefix, batch))
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedQuery embeds one query with nomic's "search_query: " prefix.
func (c *Client) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.embed(ctx, []string{queryPrefix + text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// withPrefix returns texts with prefix prepended to each.
func withPrefix(prefix string, texts []string) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = prefix + t
	}
	return out
}

// embed makes one /embeddings request (with retries for transient errors) and returns the vectors in input
// order, checking that every input got exactly one vector of domain.EmbeddingDim floats.
func (c *Client) embed(ctx context.Context, input []string) ([][]float32, error) {
	body := map[string]any{"model": c.cfg.EmbedModel, "input": input, "encoding_format": "float"}
	a := attempt{slot: domain.ModelSlotEmbed, path: embeddingsPath, timeout: c.cfg.Timeout}
	var resp embeddingsResponse
	err := c.retry(ctx, max(c.cfg.MaxRetries, 0), func() error {
		resp = embeddingsResponse{}
		return c.post(ctx, a, body, &resp)
	})
	if err != nil {
		return nil, err
	}
	c.record(ctx, resp.Usage.toUsage(domain.ModelSlotEmbed, modelOf(resp.Model, c.cfg.EmbedModel)))
	return orderEmbeddings(resp, len(input))
}

// orderEmbeddings places each returned vector at its input index and checks the count and dimensions.
func orderEmbeddings(resp embeddingsResponse, n int) ([][]float32, error) {
	if len(resp.Data) != n {
		return nil, fmt.Errorf("%w: gateway %s: got %d embeddings for %d inputs",
			domain.ErrUnavailable, embeddingsPath, len(resp.Data), n)
	}
	out := make([][]float32, n)
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= n || out[d.Index] != nil {
			return nil, fmt.Errorf("%w: gateway %s: bad or duplicate embedding index %d",
				domain.ErrUnavailable, embeddingsPath, d.Index)
		}
		if len(d.Embedding) != domain.EmbeddingDim {
			return nil, fmt.Errorf("%w: gateway %s: embedding has %d dimensions, want %d",
				domain.ErrUnavailable, embeddingsPath, len(d.Embedding), domain.EmbeddingDim)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
