package fake

import (
	"context"
	"fmt"
	"math"
	"memd/internal/ai"
	"memd/internal/domain"
	"slices"
	"sync"
)

// RerankCall records one Reranker call.
type RerankCall struct {
	// Binding is the binding the caller passed.
	Binding domain.ModelBinding
	// Query is the query text.
	Query string
	// Docs are the documents, in order.
	Docs []string
}

// rerankPair keys a scripted (query, doc) score.
type rerankPair struct{ query, doc string }

// Reranker is a scriptable ai.Reranker. Each doc's raw score is, in order of precedence: the SetScore
// score for the exact (query, doc); else the SetDocScore score for doc; else SetFunc's function; else the
// default, the fraction of the query's distinct lowercase tokens (runs of letters and digits) that appear
// in doc, in [0,1] (0 for a query with no tokens). Like the production adapter, if any raw score is
// outside [0,1] the logistic sigmoid 1/(1+e^-x) is applied to all scores (order is preserved); otherwise
// they pass through unchanged. Every call is recorded (see Calls), including calls that fail because of
// FailWith or a done ctx. It is safe for concurrent use.
type Reranker struct {
	mu    sync.Mutex
	pair  map[rerankPair]float32
	doc   map[string]float32
	fn    func(query, doc string) float32
	fail  error
	calls []RerankCall
}

var _ ai.Reranker = (*Reranker)(nil)

// NewReranker returns a Reranker with no scripted scores, scoring by token overlap.
func NewReranker() *Reranker {
	return &Reranker{pair: map[rerankPair]float32{}, doc: map[string]float32{}}
}

// SetScore scripts the raw score of doc under exactly query; it wins over SetDocScore and SetFunc.
func (r *Reranker) SetScore(query, doc string, score float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pair[rerankPair{query, doc}] = score
}

// SetDocScore scripts the raw score of doc under any query that has no SetScore score for it.
func (r *Reranker) SetDocScore(doc string, score float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doc[doc] = score
}

// SetFunc sets the scorer for docs without a scripted score; nil restores the token-overlap default.
// f is called without the Reranker's lock held.
func (r *Reranker) SetFunc(f func(query, doc string) float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fn = f
}

// FailWith makes every later Rerank return an error wrapping err (errors.Is matches) until FailWith(nil).
func (r *Reranker) FailWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

// Calls returns a copy of the recorded calls in call order.
func (r *Reranker) Calls() []RerankCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RerankCall, len(r.calls))
	for i, c := range r.calls {
		c.Docs = slices.Clone(c.Docs)
		out[i] = c
	}
	return out
}

// Rerank scores each doc against query, one score per doc in order (see Reranker for the rules).
func (r *Reranker) Rerank(ctx context.Context, b domain.ModelBinding, query string, docs []string) ([]float32, error) {
	scores, fn, err := r.scripted(ctx, RerankCall{Binding: b, Query: query, Docs: slices.Clone(docs)})
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(docs))
	squash := false
	for i, d := range docs {
		s, ok := scores[i]
		switch {
		case ok:
		case fn != nil:
			s = fn(query, d)
		default:
			s = rerankTokenOverlap(query, d)
		}
		out[i] = s
		squash = squash || s < 0 || s > 1
	}
	if squash {
		for i, s := range out {
			out[i] = float32(1 / (1 + math.Exp(-float64(s))))
		}
	}
	return out, nil
}

// scripted records c, reports a done ctx or the FailWith error, and otherwise returns the scripted raw
// score of each doc index that has one plus the current fallback function.
func (r *Reranker) scripted(ctx context.Context, c RerankCall) (map[int]float32, func(string, string) float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("fake reranker: %w", err)
	}
	if r.fail != nil {
		return nil, nil, fmt.Errorf("fake reranker: %w", r.fail)
	}
	scores := map[int]float32{}
	for i, d := range c.Docs {
		if s, ok := r.pair[rerankPair{c.Query, d}]; ok {
			scores[i] = s
		} else if s, ok := r.doc[d]; ok {
			scores[i] = s
		}
	}
	return scores, r.fn, nil
}

// rerankTokenOverlap is the fraction of query's distinct tokens that also occur in doc; 0 when query has none.
func rerankTokenOverlap(query, doc string) float32 {
	qs := map[string]struct{}{}
	for _, t := range modelTokens(query) {
		qs[t] = struct{}{}
	}
	if len(qs) == 0 {
		return 0
	}
	ds := map[string]struct{}{}
	for _, t := range modelTokens(doc) {
		ds[t] = struct{}{}
	}
	hit := 0
	for t := range qs {
		if _, ok := ds[t]; ok {
			hit++
		}
	}
	return float32(hit) / float32(len(qs))
}
