package linking

import (
	"cmp"
	"context"
	"math"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"slices"
	"time"
)

// CauseMap maps an effect unit to the units that caused it, from the batch-local domain.ExtractedFact.Causes
// indexes once the units have ids. Nil = no causal edges.
type CauseMap map[domain.UnitID][]domain.UnitID

// factTypes are the unit types links join: facts, never observations.
var factTypes = []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience}

// minTemporalWeight is the floor of a temporal link's weight (Hindsight).
const minTemporalWeight = 0.3

// Linker adds graph edges for freshly inserted (or re-derived) units, through the transaction's searches and its
// graph (store.Tx.Graph). Temporal and semantic links are symmetric and written in both directions (as in
// Hindsight), so the forward-only graph arm reaches a neighbour from either end. The cap bounds the linked unit's
// own pass, counting its neighbours (links touching it / 2); a neighbour's side is not capped. Causal links run
// cause -> effect and are not capped.
type Linker struct{}

// NewLinker returns the Linker.
func NewLinker() *Linker { return &Linker{} }

// Link adds temporal + semantic (kNN >= threshold) + causal edges for freshly inserted units, within the per-unit
// caps (temporal_link_cap, semantic_link_k). It runs in the caller's transaction and reads the tuning from the
// bank's stored config (tenant < bank; per-request overrides need LinkWith).
func (l *Linker) Link(ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes CauseMap) error {
	tenant, err := tx.Banks().TenantConfig(ctx, b.Tenant)
	if err != nil {
		return err
	}
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return err
	}
	cfg := domain.BankConfig{Retain: mergeTuning(tenant.Retain, bank.Config.Retain)}
	return l.LinkWith(ctx, tx, b, units, causes, cfg)
}

// LinkWith is Link with the caller's effective config (a retain's request and item overrides).
func (l *Linker) LinkWith(
	ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes CauseMap, cfg domain.BankConfig,
) error {
	run := &linkRun{tx: tx, b: b, t: tuningOf(cfg), counts: map[countKey]int{}, done: map[pairKey]bool{}}
	for _, u := range units {
		if err := run.semantic(ctx, u); err != nil {
			return err
		}
		if err := run.temporal(ctx, u); err != nil {
			return err
		}
	}
	var causal []graph.Link
	for _, u := range units {
		for _, c := range causes[u.ID] {
			if c != u.ID {
				causal = append(causal, graph.Link{Bank: b, From: c, To: u.ID, Kind: graph.KindCausal, Weight: 1})
			}
		}
	}
	if len(causal) == 0 {
		return nil
	}
	return tx.Graph().AddLinks(ctx, causal)
}

// countKey caches one unit's link count of one kind.
type countKey struct {
	unit domain.UnitID
	kind graph.Kind
}

// pairKey is an unordered unit pair of one kind.
type pairKey struct {
	a, b domain.UnitID
	kind graph.Kind
}

// newPairKey orders the pair.
func newPairKey(x, y domain.UnitID, kind graph.Kind) pairKey {
	if y < x {
		x, y = y, x
	}
	return pairKey{a: x, b: y, kind: kind}
}

// linkRun is one Link call: the tuning plus cached link counts.
type linkRun struct {
	tx     store.Tx
	b      domain.BankRef
	t      tuning
	counts map[countKey]int
	done   map[pairKey]bool
}

// neighbours returns how many symmetric links of kind u has (links touching it / 2, rounded up).
func (r *linkRun) neighbours(ctx context.Context, u domain.UnitID, kind graph.Kind) (int, error) {
	k := countKey{u, kind}
	n, ok := r.counts[k]
	if !ok {
		var err error
		if n, err = r.tx.Graph().Degree(ctx, u, kind); err != nil {
			return 0, err
		}
		r.counts[k] = n
	}
	return (n + 1) / 2, nil
}

// connect writes the symmetric pair u <-> v while u (the unit being linked) is below capLimit; it reports
// whether u still has room. Only u's side is capped, as in Hindsight's _cap_links_per_unit: a neighbour at
// its cap still takes the pair, so later units in a dense cluster are not starved of links.
func (r *linkRun) connect(
	ctx context.Context, u, v domain.UnitID, kind graph.Kind, w float64, capLimit int,
) (bool, error) {
	pk := newPairKey(u, v, kind)
	if u == v || r.done[pk] {
		return true, nil
	}
	nu, err := r.neighbours(ctx, u, kind)
	if err != nil || nu >= capLimit {
		return false, err
	}
	w = math.Max(0, math.Min(1, w))
	err = r.tx.Graph().AddLinks(ctx, []graph.Link{
		{Bank: r.b, From: u, To: v, Kind: kind, Weight: w},
		{Bank: r.b, From: v, To: u, Kind: kind, Weight: w},
	})
	if err != nil {
		return false, err
	}
	r.done[pk] = true
	r.counts[countKey{u, kind}] += 2
	r.counts[countKey{v, kind}] += 2
	return nu+1 < capLimit, nil
}

// semantic links u to its nearest facts at or above the similarity floor, up to semantic_link_k each.
func (r *linkRun) semantic(ctx context.Context, u domain.Unit) error {
	k := r.t.semanticK
	if k <= 0 || len(u.Embedding) == 0 {
		return nil
	}
	hits, err := r.tx.SearchVector(ctx, store.VectorQuery{
		Bank: r.b, Embedding: u.Embedding, Types: factTypes, MinSimilarity: r.t.semanticMinSim, Limit: 2*k + 1,
	})
	if err != nil {
		return err
	}
	for _, h := range hits {
		if h.Score < r.t.semanticMinSim {
			continue
		}
		room, err := r.connect(ctx, u.ID, h.ID, graph.KindSemantic, h.Score, k)
		if err != nil || !room {
			return err
		}
	}
	return nil
}

// temporal links u to the facts nearest in time within the window, up to temporal_link_cap each; the weight
// falls linearly from 1 to 0.3 across the window. Timeless units have no temporal links.
func (r *linkRun) temporal(ctx context.Context, u domain.Unit) error {
	at := anchor(u)
	capLimit, w := r.t.temporalCap, r.t.temporalWindow
	if at == nil || capLimit <= 0 {
		return nil
	}
	hits, err := r.tx.SearchTemporal(ctx, store.TemporalQuery{
		Bank: r.b, Window: domain.TimeWindow{Start: at.Add(-w), End: at.Add(w)}, Types: factTypes, Limit: 2*capLimit + 1,
	})
	if err != nil {
		return err
	}
	ids := make([]domain.UnitID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	near, err := r.tx.GetUnits(ctx, r.b, ids)
	if err != nil {
		return err
	}
	type cand struct {
		id  domain.UnitID
		gap time.Duration
	}
	cands := make([]cand, 0, len(near))
	for _, n := range near {
		if t := anchor(n); t != nil && n.ID != u.ID {
			if gap := absDuration(t.Sub(*at)); gap <= w {
				cands = append(cands, cand{n.ID, gap})
			}
		}
	}
	slices.SortStableFunc(cands, func(x, y cand) int { return cmp.Or(cmp.Compare(x.gap, y.gap), cmp.Compare(x.id, y.id)) })
	for _, c := range cands {
		weight := math.Max(minTemporalWeight, 1-c.gap.Hours()/w.Hours())
		room, err := r.connect(ctx, u.ID, c.id, graph.KindTemporal, weight, capLimit)
		if err != nil || !room {
			return err
		}
	}
	return nil
}

// anchor is a unit's time for temporal links: when it occurred, else when it was said; nil when timeless.
func anchor(u domain.Unit) *time.Time {
	if u.Timeless {
		return nil
	}
	if u.OccurredStart != nil {
		return u.OccurredStart
	}
	return u.MentionedAt
}

// absDuration is |d|.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// Link tuning defaults when retain leaves a knob unset (Hindsight's defaults).
const (
	// DefaultTemporalLinkWindowHours bounds temporal links (Hindsight: 24 h).
	DefaultTemporalLinkWindowHours = 24
	// DefaultSemanticLinkK is the semantic neighbours per unit and the semantic link cap (Hindsight: top 50).
	DefaultSemanticLinkK = 50
	// DefaultSemanticLinkMinSimilarity is the cosine floor of a semantic link (Hindsight: 0.7).
	DefaultSemanticLinkMinSimilarity = 0.7
	// DefaultTemporalLinkCap caps temporal links per unit (spec: 20).
	DefaultTemporalLinkCap = 20
)

// tuning is the link part of retain's config with every default applied.
type tuning struct {
	temporalWindow         time.Duration
	temporalCap, semanticK int
	semanticMinSim         float64
}

// tuningOf resolves cfg.Retain's link knobs against the defaults.
func tuningOf(cfg domain.BankConfig) tuning {
	r := domain.RetainConfig{}
	if cfg.Retain != nil {
		r = *cfg.Retain
	}
	return tuning{
		temporalWindow: time.Duration(domain.PositiveOr(r.TemporalLinkWindowHours, DefaultTemporalLinkWindowHours)) * time.Hour,
		temporalCap:    domain.NonNegativeOr(r.TemporalLinkCap, DefaultTemporalLinkCap),
		semanticK:      domain.NonNegativeOr(r.SemanticLinkK, DefaultSemanticLinkK),
		semanticMinSim: domain.ValueOr(r.SemanticLinkMinSimilarity, DefaultSemanticLinkMinSimilarity),
	}
}

// mergeTuning merges retain config levels field by field, later levels winning.
func mergeTuning(levels ...*domain.RetainConfig) *domain.RetainConfig {
	out := domain.RetainConfig{}
	for _, l := range levels {
		if l == nil {
			continue
		}
		out.ChunkMaxTokens = cmp.Or(l.ChunkMaxTokens, out.ChunkMaxTokens)
		out.ChunkOverlapTokens = cmp.Or(l.ChunkOverlapTokens, out.ChunkOverlapTokens)
		out.EntityMatchThreshold = cmp.Or(l.EntityMatchThreshold, out.EntityMatchThreshold)
		out.TemporalLinkWindowHours = cmp.Or(l.TemporalLinkWindowHours, out.TemporalLinkWindowHours)
		out.SemanticLinkK = cmp.Or(l.SemanticLinkK, out.SemanticLinkK)
		out.SemanticLinkMinSimilarity = cmp.Or(l.SemanticLinkMinSimilarity, out.SemanticLinkMinSimilarity)
		out.ExtractCausalLinks = cmp.Or(l.ExtractCausalLinks, out.ExtractCausalLinks)
		out.StructuredChunkMaxTokens = cmp.Or(l.StructuredChunkMaxTokens, out.StructuredChunkMaxTokens)
		out.TemporalLinkCap = cmp.Or(l.TemporalLinkCap, out.TemporalLinkCap)
	}
	return &out
}
