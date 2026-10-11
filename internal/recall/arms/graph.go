package arms

import (
	"context"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/recall"
	"memd/internal/store"
)

const (
	// graphSeedLimit and graphSeedMinSimilarity pick the graph arm's entry points (Hindsight's GRAPH_SEED_LIMIT and
	// graph_seed_min_similarity).
	graphSeedLimit         = 20
	graphSeedMinSimilarity = 0.3
	// defaultGraphMaxHops is recall.graph_max_hops when unset.
	defaultGraphMaxHops = 1
)

// graphLinkKinds are the links the graph arm follows: semantic and causal, never temporal (temporal proximity is
// the temporal arm's job).
var graphLinkKinds = []graph.Kind{graph.KindSemantic, graph.KindCausal}

// Graph is the "graph" arm: units linked to the query's best vector matches.
type Graph struct {
	s store.Search
	g graph.Reader
}

var _ recall.Arm = (*Graph)(nil)

// NewGraph returns the graph arm: seeds from s (vector search), expansion over g.
func NewGraph(s store.Search, g graph.Reader) *Graph { return &Graph{s: s, g: g} }

// Name implements recall.Arm.
func (*Graph) Name() string { return "graph" }

// Description implements recall.Arm.
func (*Graph) Description() string {
	return "Link expansion: memories linked (semantic and causal links) to the query's best vector matches."
}

// Retrieve implements recall.Arm: the top 20 vector seeds at cosine >= 0.3, then Expand over semantic and causal
// links up to recall.graph_max_hops (default 1, clamped to 1..2), passing the query's filters as hints. The engine
// re-checks the hits against the store.
func (a *Graph) Retrieve(ctx context.Context, q recall.Query) ([]recall.Hit, error) {
	seeds, err := a.s.SearchVector(ctx, store.VectorQuery{
		Bank: q.Bank, Embedding: q.Embedding, Types: q.Types, Tags: q.Tags, MinSimilarity: graphSeedMinSimilarity,
		Seq: q.Seq, Limit: graphSeedLimit,
	})
	if err != nil || len(seeds) == 0 {
		return nil, err
	}
	ids := make([]domain.UnitID, len(seeds))
	for i, h := range seeds {
		ids[i] = h.ID
	}
	hops := defaultGraphMaxHops
	if v := q.ArmSettings(a.Name()).Recall.GraphMaxHops; v != nil {
		hops = *v
	}
	hs, err := a.g.Expand(ctx, graph.Query{
		Bank: q.Bank, Seeds: ids, MaxHops: min(max(hops, 1), 2), Kinds: graphLinkKinds, Types: q.Types, Tags: q.Tags,
		Seq: q.Seq, Limit: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]recall.Hit, len(hs))
	for i, h := range hs {
		out[i] = recall.Hit{ID: h.ID, Score: h.Score}
	}
	return out, nil
}
