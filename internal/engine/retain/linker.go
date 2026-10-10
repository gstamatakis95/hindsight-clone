package retain

import (
	"cmp"
	"context"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// Linker adds graph edges for freshly inserted (or re-derived) units.
type Linker interface {
	// Link adds temporal + semantic (kNN >= threshold) + causal edges for freshly inserted units, within
	// the per-unit caps (temporal_link_cap, semantic_link_k). It runs in the caller's transaction and reads
	// the tuning from the bank's stored config (tenant < bank; per-request overrides need LinkWith).
	Link(ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes CauseMap) error
	// LinkWith is Link with the caller's effective config (a retain's request and item overrides).
	LinkWith(
		ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes CauseMap, cfg domain.BankConfig,
	) error
}

// CauseMap maps an effect unit to the units that caused it, from the batch-local
// domain.ExtractedFact.Causes indexes once the units have ids. Nil = no causal edges. It is an alias of
// the plain map type so consumers (maintenance) can declare Linker without importing retain.
type CauseMap = map[port.UnitID][]port.UnitID

// factTypes are the unit types links join: facts, never observations.
var factTypes = []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience}

// minTemporalWeight is the floor of a temporal link's weight (Hindsight).
const minTemporalWeight = 0.3

// linker is the Linker over the transaction's searches and LinkRepo.
type linker struct{}

var _ Linker = (*linker)(nil)

// NewLinker returns the Linker. Temporal and semantic links are symmetric and written in both directions
// (as in Hindsight), so the forward-only graph arm reaches a neighbour from either end; a unit's cap counts
// its neighbours (links touching it / 2). Causal links run cause -> effect and are not capped (decision 806).
func NewLinker() Linker { return &linker{} }

// Link implements Linker.
func (l *linker) Link(ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes CauseMap) error {
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

// LinkWith implements Linker.
func (l *linker) LinkWith(
	ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes CauseMap, cfg domain.BankConfig,
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
	var causal []domain.Link
	for _, u := range units {
		for _, c := range causes[u.ID] {
			if c != u.ID {
				causal = append(causal, domain.Link{Bank: b, From: c, To: u.ID, Kind: domain.LinkCausal, Weight: 1})
			}
		}
	}
	if len(causal) == 0 {
		return nil
	}
	return tx.Links().Insert(ctx, causal)
}

// countKey caches one unit's link count of one kind.
type countKey struct {
	unit port.UnitID
	kind domain.LinkKind
}

// pairKey is an unordered unit pair of one kind.
type pairKey struct {
	a, b port.UnitID
	kind domain.LinkKind
}

// newPairKey orders the pair.
func newPairKey(x, y port.UnitID, kind domain.LinkKind) pairKey {
	if y < x {
		x, y = y, x
	}
	return pairKey{a: x, b: y, kind: kind}
}

// linkRun is one Link call: the tuning plus cached link counts.
type linkRun struct {
	tx     port.Tx
	b      port.BankRef
	t      tuning
	counts map[countKey]int
	done   map[pairKey]bool
}

// neighbours returns how many symmetric links of kind u has (links touching it / 2, rounded up).
func (r *linkRun) neighbours(ctx context.Context, u port.UnitID, kind domain.LinkKind) (int, error) {
	k := countKey{u, kind}
	n, ok := r.counts[k]
	if !ok {
		var err error
		if n, err = r.tx.Links().Count(ctx, u, kind); err != nil {
			return 0, err
		}
		r.counts[k] = n
	}
	return (n + 1) / 2, nil
}

// connect writes the symmetric pair u <-> v when neither is at capLimit; it reports whether u still has room.
func (r *linkRun) connect(
	ctx context.Context, u, v port.UnitID, kind domain.LinkKind, w float64, capLimit int,
) (bool, error) {
	pk := newPairKey(u, v, kind)
	if u == v || r.done[pk] {
		return true, nil
	}
	nu, err := r.neighbours(ctx, u, kind)
	if err != nil || nu >= capLimit {
		return false, err
	}
	nv, err := r.neighbours(ctx, v, kind)
	if err != nil || nv >= capLimit {
		return true, err
	}
	w = math.Max(0, math.Min(1, w))
	err = r.tx.Links().Insert(ctx, []domain.Link{
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
	hits, err := r.tx.SearchVector(ctx, port.VectorQuery{
		Bank: r.b, Embedding: u.Embedding, Types: factTypes, MinSimilarity: r.t.semanticMinSim, Limit: 2*k + 1,
	})
	if err != nil {
		return err
	}
	for _, h := range hits {
		if h.Score < r.t.semanticMinSim {
			continue
		}
		room, err := r.connect(ctx, u.ID, h.ID, domain.LinkSemantic, h.Score, k)
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
	hits, err := r.tx.SearchTemporal(ctx, port.TemporalQuery{
		Bank: r.b, Window: domain.TimeWindow{Start: at.Add(-w), End: at.Add(w)}, Types: factTypes, Limit: 2*capLimit + 1,
	})
	if err != nil {
		return err
	}
	ids := make([]port.UnitID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	near, err := r.tx.GetUnits(ctx, r.b, ids)
	if err != nil {
		return err
	}
	type cand struct {
		id  port.UnitID
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
		room, err := r.connect(ctx, u.ID, c.id, domain.LinkTemporal, weight, capLimit)
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

// mergeTuning merges RetainTuning levels field by field, later levels winning.
func mergeTuning(levels ...*domain.RetainTuning) *domain.RetainTuning {
	out := domain.RetainTuning{}
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
