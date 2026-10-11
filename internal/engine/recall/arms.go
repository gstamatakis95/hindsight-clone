package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
)

// semantic is the HNSW cosine arm.
type semantic struct{ r store.Reader }

// keyword is the BM25 arm.
type keyword struct{ r store.Reader }

// graph is the link-expansion arm.
type graph struct{ r store.Reader }

// temporal is the occurrence-overlap arm.
type temporal struct{ r store.Reader }

var (
	_ Retriever = (*semantic)(nil)
	_ Retriever = (*keyword)(nil)
	_ Retriever = (*graph)(nil)
	_ Retriever = (*temporal)(nil)
)

// graphLinkKinds are the links the graph arm follows: semantic and causal, never temporal (temporal
// proximity is the temporal arm's job; decision 901).
var graphLinkKinds = []domain.LinkKind{domain.LinkSemantic, domain.LinkCausal}

// newSemantic returns the semantic arm: SearchVector with the min_scores.semantic floor (default 0.3, as
// Hindsight; an explicit 0 disables it).
func newSemantic(r store.Reader) Retriever { return &semantic{r: r} }

// newKeyword returns the keyword arm: SearchText (BM25) with the min_scores.keyword floor.
func newKeyword(r store.Reader) Retriever { return &keyword{r: r} }

// newGraph returns the graph arm: its own semantic seeds (the top 20 at cosine >= 0.3, as Hindsight), then
// ExpandGraph over semantic and causal links up to graph_max_hops (default 1).
func newGraph(r store.Reader) Retriever { return &graph{r: r} }

// newTemporal returns the temporal arm: SearchTemporal over the query window, spread over temporal_buckets
// (default 4). It returns nothing when the query has no window.
func newTemporal(r store.Reader) Retriever { return &temporal{r: r} }

// Name implements Retriever.
func (*semantic) Name() string { return ArmSemantic }

// Name implements Retriever.
func (*keyword) Name() string { return ArmKeyword }

// Name implements Retriever.
func (*graph) Name() string { return ArmGraph }

// Name implements Retriever.
func (*temporal) Name() string { return ArmTemporal }

// Retrieve implements Retriever.
func (a *semantic) Retrieve(ctx context.Context, q *parsedQuery) ([]Candidate, error) {
	floor, ok := gate{}.ArmFloor(ArmSemantic, q.MinScores)
	if !ok {
		floor = semanticMinSimilarity // an explicit 0 disables it
	}
	hs, err := a.r.SearchVector(ctx, store.VectorQuery{
		Bank: q.Bank, Embedding: q.Embedding, Types: q.Types, Tags: q.Predicate, MinSimilarity: floor,
		Seq: q.Seq, Limit: q.Limit,
	})
	return candidates(ArmSemantic, hs, q.Limit), err
}

// Retrieve implements Retriever.
func (a *keyword) Retrieve(ctx context.Context, q *parsedQuery) ([]Candidate, error) {
	floor, _ := gate{}.ArmFloor(ArmKeyword, q.MinScores)
	hs, err := a.r.SearchText(ctx, store.TextQuery{
		Bank: q.Bank, Text: q.Text, Types: q.Types, Tags: q.Predicate, MinScore: floor, Seq: q.Seq, Limit: q.Limit,
	})
	return candidates(ArmKeyword, hs, q.Limit), err
}

// Retrieve implements Retriever.
func (a *graph) Retrieve(ctx context.Context, q *parsedQuery) ([]Candidate, error) {
	seeds, err := a.r.SearchVector(ctx, store.VectorQuery{
		Bank: q.Bank, Embedding: q.Embedding, Types: q.Types, Tags: q.Predicate,
		MinSimilarity: graphSeedMinSimilarity, Seq: q.Seq, Limit: graphSeedLimit,
	})
	if err != nil || len(seeds) == 0 {
		return nil, err
	}
	ids := make([]domain.UnitID, len(seeds))
	for i, h := range seeds {
		ids[i] = h.ID
	}
	hs, err := a.r.ExpandGraph(ctx, store.GraphQuery{
		Bank: q.Bank, Seeds: ids, MaxHops: settingsOf(q.Config).graphMaxHops, Kinds: graphLinkKinds,
		Types: q.Types, Tags: q.Predicate, Seq: q.Seq, Limit: q.Limit,
	})
	return candidates(ArmGraph, hs, q.Limit), err
}

// Retrieve implements Retriever.
func (a *temporal) Retrieve(ctx context.Context, q *parsedQuery) ([]Candidate, error) {
	if q.Window == nil {
		return nil, nil
	}
	hs, err := a.r.SearchTemporal(ctx, store.TemporalQuery{
		Bank: q.Bank, Window: *q.Window, Buckets: settingsOf(q.Config).temporalBuckets, Types: q.Types,
		Tags: q.Predicate, Seq: q.Seq, Limit: q.Limit, Embedding: q.Embedding,
		MinSimilarity: temporalMinSimilarity,
	})
	return candidates(ArmTemporal, hs, q.Limit), err
}

// candidates turns an arm's hits (best first) into candidates carrying their 1-based rank and raw score,
// keeping at most limit (<= 0 = all) and dropping repeated ids.
func candidates(arm string, hs []store.Hit, limit int) []Candidate {
	if limit > 0 && len(hs) > limit {
		hs = hs[:limit]
	}
	out := make([]Candidate, 0, len(hs))
	seen := make(map[domain.UnitID]bool, len(hs))
	for _, h := range hs {
		if seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		out = append(out, Candidate{ID: h.ID, Arms: map[string]armScore{arm: {Rank: len(out) + 1, Score: h.Score}}})
	}
	return out
}
