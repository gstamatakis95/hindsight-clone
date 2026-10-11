package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"slices"
)

// semantic is the HNSW cosine arm.
type semantic struct{ r store.Reader }

// keyword is the BM25 arm.
type keyword struct{ r store.Reader }

// graphArm is the link-expansion arm.
type graphArm struct {
	r store.Reader
	g graph.Reader
}

// temporal is the occurrence-overlap arm.
type temporal struct{ r store.Reader }

var (
	_ retriever = (*semantic)(nil)
	_ retriever = (*keyword)(nil)
	_ retriever = (*graphArm)(nil)
	_ retriever = (*temporal)(nil)
)

// graphLinkKinds are the links the graph arm follows: semantic and causal, never temporal (temporal
// proximity is the temporal arm's job; decision 901).
var graphLinkKinds = []graph.Kind{graph.KindSemantic, graph.KindCausal}

// newSemantic returns the semantic arm: SearchVector with the min_scores.semantic floor (default 0.3, as
// Hindsight; an explicit 0 disables it).
func newSemantic(r store.Reader) retriever { return &semantic{r: r} }

// newKeyword returns the keyword arm: SearchText (BM25) with the min_scores.keyword floor.
func newKeyword(r store.Reader) retriever { return &keyword{r: r} }

// newGraph returns the graph arm: its own semantic seeds (the top 20 at cosine >= 0.3, as Hindsight), then g's
// Expand over semantic and causal links up to graph_max_hops (default 1), re-checked against r.
func newGraph(r store.Reader, g graph.Reader) retriever { return &graphArm{r: r, g: g} }

// newTemporal returns the temporal arm: SearchTemporal over the query window, spread over temporal_buckets
// (default 4). It returns nothing when the query has no window.
func newTemporal(r store.Reader) retriever { return &temporal{r: r} }

// Name implements retriever.
func (*semantic) Name() string { return ArmSemantic }

// Name implements retriever.
func (*keyword) Name() string { return ArmKeyword }

// Name implements retriever.
func (*graphArm) Name() string { return ArmGraph }

// Name implements retriever.
func (*temporal) Name() string { return ArmTemporal }

// Retrieve implements retriever.
func (a *semantic) Retrieve(ctx context.Context, q *parsedQuery) ([]candidate, error) {
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

// Retrieve implements retriever.
func (a *keyword) Retrieve(ctx context.Context, q *parsedQuery) ([]candidate, error) {
	floor, _ := gate{}.ArmFloor(ArmKeyword, q.MinScores)
	hs, err := a.r.SearchText(ctx, store.TextQuery{
		Bank: q.Bank, Text: q.Text, Types: q.Types, Tags: q.Predicate, MinScore: floor, Seq: q.Seq, Limit: q.Limit,
	})
	return candidates(ArmKeyword, hs, q.Limit), err
}

// Retrieve implements retriever.
func (a *graphArm) Retrieve(ctx context.Context, q *parsedQuery) ([]candidate, error) {
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
	hs, err := a.g.Expand(ctx, graph.Query{
		Bank: q.Bank, Seeds: ids, MaxHops: settingsOf(q.Config).graphMaxHops, Kinds: graphLinkKinds,
		Types: q.Types, Tags: q.Predicate, Seq: q.Seq, Limit: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	kept, err := a.recheck(ctx, q, hs)
	return candidates(ArmGraph, kept, q.Limit), err
}

// recheck keeps the graph hits that are live units of the bank passing q's types, tags and seq, in hit order with
// their scores. The graph treats those filters as hints and may lag the store; Postgres applies them natively, so
// for it this removes nothing.
func (a *graphArm) recheck(ctx context.Context, q *parsedQuery, hs []graph.Hit) ([]store.Hit, error) {
	if len(hs) == 0 {
		return nil, nil
	}
	ids := make([]domain.UnitID, len(hs))
	for i, h := range hs {
		ids[i] = h.ID
	}
	units, err := a.r.GetUnits(ctx, q.Bank, ids)
	if err != nil {
		return nil, err
	}
	live := make(map[domain.UnitID]domain.Unit, len(units))
	for _, u := range units {
		live[u.ID] = u
	}
	out := make([]store.Hit, 0, len(hs))
	for _, h := range hs {
		u, ok := live[h.ID]
		if ok && (len(q.Types) == 0 || slices.Contains(q.Types, u.Type)) && q.Predicate.Eval(u.Tags) &&
			q.Seq.Contains(u.Seq) {
			out = append(out, store.Hit{ID: h.ID, Score: h.Score})
		}
	}
	return out, nil
}

// Retrieve implements retriever.
func (a *temporal) Retrieve(ctx context.Context, q *parsedQuery) ([]candidate, error) {
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
func candidates(arm string, hs []store.Hit, limit int) []candidate {
	if limit > 0 && len(hs) > limit {
		hs = hs[:limit]
	}
	out := make([]candidate, 0, len(hs))
	seen := make(map[domain.UnitID]bool, len(hs))
	for _, h := range hs {
		if seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		out = append(out, candidate{ID: h.ID, Arms: map[string]armScore{arm: {Rank: len(out) + 1, Score: h.Score}}})
	}
	return out
}
