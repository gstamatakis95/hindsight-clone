package arms

import (
	"context"
	"memd/internal/recall"
	"memd/internal/store"
)

// vectorMinSimilarity is the vector arm's floor when min_scores.arms has no "vector" entry (Hindsight's 0.3).
const vectorMinSimilarity = 0.3

// Vector is the "vector" arm: HNSW cosine similarity between the query and unit embeddings.
type Vector struct{ s store.Search }

var _ recall.Arm = (*Vector)(nil)

// NewVector returns the vector arm over s.
func NewVector(s store.Search) *Vector { return &Vector{s: s} }

// Name implements recall.Arm.
func (*Vector) Name() string { return "vector" }

// Description implements recall.Arm.
func (*Vector) Description() string {
	return "Semantic similarity: cosine between the query embedding and each memory's embedding."
}

// Retrieve implements recall.Arm: SearchVector with the min_scores floor (default 0.3; an explicit 0 disables it).
func (a *Vector) Retrieve(ctx context.Context, q recall.Query) ([]recall.Hit, error) {
	floor := vectorMinSimilarity
	if m := q.ArmSettings(a.Name()).MinScore; m != nil {
		floor = *m
	}
	hs, err := a.s.SearchVector(ctx, store.VectorQuery{
		Bank: q.Bank, Embedding: q.Embedding, Types: q.Types, Tags: q.Tags, MinSimilarity: floor, Seq: q.Seq,
		Limit: q.Limit,
	})
	return hits(hs), err
}
