package gateway

import (
	"context"
	"fmt"
	"maps"
	"math"
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
	err := c.traced(ctx, a.slot, c.cfg.EmbedModel, a.path, func(ctx context.Context) error {
		return c.retry(ctx, max(c.cfg.MaxRetries, 0), func() error {
			resp = embeddingsResponse{}
			return c.post(ctx, a, body, &resp)
		})
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

// rerankResponse is the subset of a Cohere/Jina-style rerank response the client reads.
type rerankResponse struct {
	// Model is the model that served the call.
	Model string `json:"model"`
	// Results holds one score per scored document, tagged with the document's index.
	Results []struct {
		// Index is the position of the document in the request.
		Index int `json:"index"`
		// RelevanceScore is the raw score: a calibrated probability or a cross-encoder logit.
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
	// Usage is the token usage, when the route reports it.
	Usage *apiUsage `json:"usage"`
}

// Rerank scores each doc against query with the binding's cross-encoder through the gateway's rerank route
// (Cohere/Jina-style body), one score per doc in order. If every score is in [0,1] the scores pass through;
// otherwise every score goes through a sigmoid (Hindsight's rule). A doc the route left unscored, or scored
// NaN, gets 0.
func (c *Client) Rerank(ctx context.Context, b domain.ModelBinding, query string, docs []string) ([]float32, error) {
	if len(docs) == 0 {
		return []float32{}, nil
	}
	body := map[string]any{
		"model": b.Model, "query": query, "documents": docs, "top_n": len(docs), "return_documents": false,
	}
	maps.Copy(body, b.Extra)
	a := attempt{slot: domain.ModelSlotRerank, path: c.cfg.RerankPath, timeout: c.timeoutFor(b)}
	var resp rerankResponse
	err := c.traced(ctx, a.slot, b.Model, a.path, func(ctx context.Context) error {
		return c.retry(ctx, c.retriesFor(b), func() error {
			resp = rerankResponse{}
			return c.post(ctx, a, body, &resp)
		})
	})
	if err != nil {
		return nil, err
	}
	c.record(ctx, resp.Usage.toUsage(domain.ModelSlotRerank, modelOf(resp.Model, b.Model)))
	raw := make([]float64, len(docs))
	scored := make([]bool, len(docs))
	for _, r := range resp.Results {
		if r.Index < 0 || r.Index >= len(docs) {
			return nil, fmt.Errorf("%w: gateway %s: result index %d out of range for %d documents",
				domain.ErrUnavailable, a.path, r.Index, len(docs))
		}
		raw[r.Index], scored[r.Index] = r.RelevanceScore, !math.IsNaN(r.RelevanceScore)
	}
	return normalizeScores(raw, scored), nil
}

// normalizeScores passes scores through when every scored value is in [0,1] and applies a sigmoid to all of
// them otherwise. Unscored entries are 0 either way.
func normalizeScores(raw []float64, scored []bool) []float32 {
	inRange := true
	for i, s := range raw {
		if scored[i] && (s < 0 || s > 1) {
			inRange = false
			break
		}
	}
	out := make([]float32, len(raw))
	for i, s := range raw {
		switch {
		case !scored[i]:
			out[i] = 0
		case inRange:
			out[i] = float32(s)
		default:
			out[i] = float32(1 / (1 + math.Exp(-s)))
		}
	}
	return out
}
