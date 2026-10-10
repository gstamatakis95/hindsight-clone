package gateway

import (
	"context"
	"fmt"
	"maps"
	"math"
	"memd/internal/domain"
)

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
	err := c.retry(ctx, c.retriesFor(b), func() error {
		resp = rerankResponse{}
		return c.post(ctx, a, body, &resp)
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
