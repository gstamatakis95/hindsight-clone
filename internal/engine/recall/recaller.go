package recall

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/timex"
	"memd/internal/port"

	"golang.org/x/sync/errgroup"
)

// Recaller answers one recall request.
type Recaller interface {
	// Recall resolves the effective config (unknown bank = domain.ErrNotFound), parses, retrieves over the
	// enabled arms concurrently, fuses, scores, gates, packs and enriches. It is read-only and opens a short
	// read transaction only to resolve config, so callers must not call it inside Store.InTx. A failing arm,
	// embedder or reranker fails the call; a recall that finds nothing is not a failure.
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
}

// RecallerDeps are the Recaller's dependencies.
type RecallerDeps struct {
	// Reader serves the four arms, hydration and source facts.
	Reader port.Reader
	// Lookups serves fuzzy tags, chunks and entity state (CCR WP-09-1); nil = those features are off.
	Lookups Lookups
	// Config merges server < tenant < bank < request.
	Config port.ConfigResolver
	// Embedder embeds the query.
	Embedder port.Embedder
	// Reranker is the cross-encoder; nil = recall.rerank off.
	Reranker port.Reranker
	// Router resolves the RERANK binding.
	Router port.ModelRouter
	// Times derives a query's time window (TIME_PARSE only for a cue its rules can't resolve); nil = no
	// derived windows (explicit windows still work).
	Times timex.TimeNormalizer
	// Clock is the reference for "now" and durations.
	Clock port.Clock
	// Count counts tokens for packing, include budgets and the query limit.
	Count TokenCounter
	// Retrievers replaces the four standard arms over Reader; nil = NewSemantic, NewKeyword, NewGraph and
	// NewTemporal.
	Retrievers []Retriever
}

// recaller is the Recaller.
type recaller struct {
	d        RecallerDeps
	arms     []Retriever
	tags     TagFilterCompiler
	fuser    Fuser
	scorer   Scorer
	gate     ScoreGate
	packer   Packer
	prefer   ObservationPreference
	includer Includer
}

var _ Recaller = (*recaller)(nil)

// NewRecaller returns the Recaller over d.
func NewRecaller(d RecallerDeps) Recaller {
	arms := d.Retrievers
	if arms == nil {
		arms = []Retriever{NewSemantic(d.Reader), NewKeyword(d.Reader), NewGraph(d.Reader), NewTemporal(d.Reader)}
	}
	var vocab TagVocabulary
	if d.Lookups != nil {
		vocab = d.Lookups
	}
	return &recaller{
		d: d, arms: arms, tags: NewTagFilterCompiler(vocab), fuser: NewRRF(),
		scorer: NewScorer(d.Reranker, d.Router, d.Clock), gate: NewScoreGate(), packer: NewPacker(d.Count),
		prefer: NewObservationPreference(), includer: NewIncluder(d.Reader, d.Lookups, d.Count),
	}
}

// Recall implements Recaller.
func (r *recaller) Recall(ctx context.Context, req RecallRequest) (RecallResult, error) {
	start := r.d.Clock.Now()
	var overrides []domain.BankConfig
	if req.Overrides != nil {
		overrides = append(overrides, *req.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, req.Bank, overrides...)
	if err != nil {
		return RecallResult{}, fmt.Errorf("recall: config: %w", err)
	}
	q, err := parse(ctx, req, cfg, parseDeps{
		embedder: r.d.Embedder, times: r.d.Times, tags: r.tags, clock: r.d.Clock, count: r.d.Count,
	})
	if err != nil {
		return RecallResult{}, err
	}
	set := settingsOf(cfg)
	lists, arms, err := r.retrieve(ctx, q, set)
	if err != nil {
		return RecallResult{}, err
	}
	fused := r.fuser.Fuse(lists, set.rrfK, set.boosts)
	ranked, reranked, err := r.rank(ctx, q, fused, set)
	if err != nil {
		return RecallResult{}, err
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	var packed []Candidate
	if req.PreferObservations {
		packed = r.prefer.Apply(ranked, maxTokens, r.d.Count)
	} else {
		packed = r.packer.Pack(ranked, maxTokens)
	}
	enr, err := r.includer.Enrich(ctx, req.Bank, packed, req.Include)
	if err != nil {
		return RecallResult{}, fmt.Errorf("recall: include: %w", err)
	}
	out := result(packed, enr)
	if req.Trace {
		out.Trace = &RecallTrace{
			Arms: arms, Fused: len(fused), Reranked: reranked, Packed: len(packed),
			TokenBudget: maxTokens, TokensUsed: tokensOf(packed), Window: q.Window,
			DurationMS: r.d.Clock.Now().Sub(start).Milliseconds(),
		}
	}
	return out, nil
}

// retrieve runs the enabled arms concurrently. The temporal arm runs only when the query has a window. It
// returns each arm's list by name and one trace entry per arm that ran, in arm order.
func (r *recaller) retrieve(ctx context.Context, q *ParsedQuery, set settings) (
	map[string][]Candidate, []ArmTrace, error,
) {
	var run []Retriever
	for _, a := range r.arms {
		if set.enabled[a.Name()] && (a.Name() != ArmTemporal || q.Window != nil) {
			run = append(run, a)
		}
	}
	results := make([][]Candidate, len(run))
	traces := make([]ArmTrace, len(run))
	g, gctx := errgroup.WithContext(ctx)
	for i, a := range run {
		g.Go(func() error {
			t0 := r.d.Clock.Now()
			cs, err := a.Retrieve(gctx, q)
			if err != nil {
				return fmt.Errorf("recall: %s arm: %w", a.Name(), err)
			}
			results[i] = cs
			traces[i] = ArmTrace{
				Arm: a.Name(), Candidates: len(cs), DurationMS: r.d.Clock.Now().Sub(t0).Milliseconds(),
				TopIDs: topIDs(cs),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("recall: retrieve: %w", err)
	}
	lists := make(map[string][]Candidate, len(run))
	for i, a := range run {
		lists[a.Name()] = results[i]
	}
	return lists, traces, nil
}

// rank keeps the top reranker_max_candidates of fused, hydrates them (units archived since retrieval are
// dropped), scores them and applies the result floors. It also returns how many the cross-encoder scored.
func (r *recaller) rank(ctx context.Context, q *ParsedQuery, fused []Candidate, set settings) (
	[]Candidate, int, error,
) {
	top := fused
	if set.rerankMax > 0 && len(top) > set.rerankMax {
		top = top[:set.rerankMax]
	}
	hydrated, err := r.hydrate(ctx, q.Bank, q.Seq, top)
	if err != nil {
		return nil, 0, err
	}
	scored, err := r.scorer.Score(ctx, q, hydrated)
	if err != nil {
		return nil, 0, fmt.Errorf("recall: score: %w", err)
	}
	kept, reranked := scored[:0:0], 0
	for _, c := range scored {
		if c.Reranker != nil {
			reranked++
		}
		if r.gate.Keep(c, q.MinScores) {
			kept = append(kept, c)
		}
	}
	return kept, reranked, nil
}

// hydrate attaches each candidate's unit, keeping fused order and dropping ids that no longer resolve or
// whose unit was rewritten outside seq since the arm saw it (a refresh's snapshot stays at or below S).
func (r *recaller) hydrate(ctx context.Context, b port.BankRef, seq port.SeqRange, cs []Candidate) (
	[]Candidate, error,
) {
	if len(cs) == 0 {
		return nil, nil
	}
	ids := make([]port.UnitID, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	units, err := r.d.Reader.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[port.UnitID]*domain.Unit, len(units))
	for i := range units {
		if seq.Contains(units[i].Seq) {
			byID[units[i].ID] = &units[i]
		}
	}
	out := make([]Candidate, 0, len(cs))
	for _, c := range cs {
		if u, ok := byID[c.ID]; ok {
			c.Unit = u
			out = append(out, c)
		}
	}
	return out, nil
}

// result builds the RecallResult from the packed candidates and the enrichment.
func result(packed []Candidate, e Enrichment) RecallResult {
	out := RecallResult{
		Results: make([]Memory, 0, len(packed)), SourceFacts: e.SourceFacts,
		SourceFactsTruncated: e.SourceFactsTruncated, Chunks: e.Chunks, Entities: e.Entities,
	}
	for _, c := range packed {
		out.Results = append(out.Results, Memory{Unit: *c.Unit, Scores: scoresOf(c)})
	}
	return out
}

// scoresOf returns a candidate's wire scores.
func scoresOf(c Candidate) Scores {
	s := Scores{Final: c.Final, Reranker: c.Reranker}
	if a, ok := c.Arms[ArmSemantic]; ok {
		s.Semantic = &a.Score
	}
	if a, ok := c.Arms[ArmKeyword]; ok {
		s.Keyword = &a.Score
	}
	return s
}

// tokensOf sums the packed candidates' token counts.
func tokensOf(cs []Candidate) int {
	n := 0
	for _, c := range cs {
		n += c.Tokens
	}
	return n
}

// topIDs returns the first traceTopIDs ids of an arm's list.
func topIDs(cs []Candidate) []port.UnitID {
	out := make([]port.UnitID, 0, min(len(cs), traceTopIDs))
	for _, c := range cs[:min(len(cs), traceTopIDs)] {
		out = append(out, c.ID)
	}
	return out
}
