package recall

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/timex"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Engine is memd's retrieval core: it answers the Recall RPC and serves reflect's recall, search_observations and
// page tools and the mental-model refreshes. It compiles tag filters (CompileTags) and searches and reads mental-model
// pages (SearchPages, ReadPages). It is read-only and safe for concurrent use; never call it inside DB.InTx.
type Engine struct {
	db       store.DB
	read     store.Reader
	config   *config.Resolver
	embedder ai.Embedder
	times    *timex.Normalizer
	clock    clock.Clock
	count    tokenCounter
	arms     registry
	tags     *tagCompiler
	fuser    rrf
	scorer   *scorer
	gate     gate
	packer   *packer
	prefer   preference
	includer *includer
}

// Deps are the Engine's dependencies.
type Deps struct {
	// DB serves hydration, includes, fuzzy tags, stale scopes and page listings through DB.Read, and whole pages
	// in a short read-only transaction.
	DB store.DB
	// Arms are the registered recall arms, in fusion order; cmd/memd/wire.go registers the built-in vector, text,
	// graph and temporal arms (internal/recall/arms). Names must be unique and non-empty.
	Arms []Arm
	// Config merges server < tenant < bank < request.
	Config *config.Resolver
	// Embedder embeds the query and page searches.
	Embedder ai.Embedder
	// Reranker is the cross-encoder; nil = recall.rerank off.
	Reranker ai.Reranker
	// Router resolves the RERANK binding.
	Router *ai.Router
	// Times derives a query's time window (TIME_PARSE only for a cue its rules can't resolve); nil = no derived
	// windows (explicit windows still work).
	Times *timex.Normalizer
	// Clock is the reference for "now" and durations.
	Clock clock.Clock
	// Tokens counts tokens for packing, include budgets and the query limit.
	Tokens *ai.Tokenizer
}

// New returns the Engine over d. An arm with an empty or repeated name is a wiring bug: New panics.
func New(d Deps) *Engine { return newEngine(d, d.DB.Read(), d.Tokens) }

// newEngine is New with the reader and the token counter chosen by the caller; tests count words.
func newEngine(d Deps, read store.Reader, count tokenCounter) *Engine {
	arms := newRegistry(d.Arms)
	return &Engine{
		db: d.DB, read: read, config: d.Config, embedder: d.Embedder, times: d.Times, clock: d.Clock, count: count,
		arms: arms, tags: newTagCompiler(read), fuser: newRRF(arms.names()), gate: newScoreGate(),
		scorer: newScorer(d.Reranker, d.Router, d.Clock, arms.windowed), packer: newPacker(count),
		prefer: newObservationPreference(), includer: newIncluder(read, count),
	}
}

// Arms lists the registered arms in fusion order (ListRecallArms).
func (r *Engine) Arms() []ArmInfo {
	out := make([]ArmInfo, len(r.arms.arms))
	for i, a := range r.arms.arms {
		out[i] = ArmInfo{Name: a.Name(), Description: a.Description()}
	}
	return out
}

// CheckArms returns domain.ErrInvalidArgument when names holds an arm name that is not registered, or a name
// twice; reflect checks its request's arms with it before its loop starts.
func (r *Engine) CheckArms(names []string) error { return r.arms.check("arms", names) }

// Recall resolves the effective config (unknown bank = domain.ErrNotFound), parses, retrieves over the selected
// arms concurrently, fuses, scores, gates, packs and enriches. It is read-only and opens a short read transaction
// only to resolve config. A failing arm, embedder or reranker fails the call; a recall that finds nothing is not a
// failure.
func (r *Engine) Recall(ctx context.Context, req Request) (Result, error) {
	start := r.clock.Now()
	if strings.TrimSpace(req.Query) == "" {
		return Result{}, fmt.Errorf("%w: recall: query is empty", domain.ErrInvalidArgument)
	}
	if err := r.arms.checkRequest(req); err != nil {
		return Result{}, fmt.Errorf("recall: %w", err)
	}
	var overrides []domain.BankConfig
	if req.Overrides != nil {
		overrides = append(overrides, *req.Overrides)
	}
	cfg, err := r.config.Effective(ctx, req.Bank, overrides...)
	if err != nil {
		return Result{}, fmt.Errorf("recall: config: %w", err)
	}
	q, err := parse(ctx, req, cfg, parseDeps{
		embedder: r.embedder, times: r.times, tags: r.tags, clock: r.clock, count: r.count,
	})
	if err != nil {
		return Result{}, err
	}
	set := settingsOf(cfg)
	lists, arms, err := r.retrieve(ctx, q, r.arms.selected(req, cfg))
	if err != nil {
		return Result{}, err
	}
	fused := r.fuser.Fuse(lists, set.rrfK)
	ranked, reranked, err := r.rank(ctx, q, fused, set)
	if err != nil {
		return Result{}, err
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	var packed []candidate
	if req.PreferObservations {
		packed = r.prefer.Apply(ranked, 2*q.Limit, maxTokens, r.count)
	} else {
		packed = r.packer.Pack(ranked, maxTokens)
	}
	enr, err := r.includer.Enrich(ctx, req.Bank, req.Seq, packed, req.Include)
	if err != nil {
		return Result{}, fmt.Errorf("recall: include: %w", err)
	}
	stale, err := r.staleScopes(ctx, req.Bank, packed)
	if err != nil {
		return Result{}, fmt.Errorf("recall: stale scopes: %w", err)
	}
	out := result(packed, enr, stale)
	if req.Trace {
		out.Trace = &Trace{
			Arms: arms, Fused: len(fused), Reranked: reranked, Packed: len(packed),
			TokenBudget: maxTokens, TokensUsed: tokensOf(packed), Window: q.Window,
			DurationMS: r.clock.Now().Sub(start).Milliseconds(),
		}
	}
	return out, nil
}

// CompileTags validates f, resolves its fuzzy leaves against b's tags by trigram similarity (at or above
// minSimilarity, recall.fuzzy_tag_min_similarity: FuzzyTagMinSimilarity) and compiles it into the tag predicate the
// searches take. An empty filter compiles to an empty PredicateAnd, which matches every unit.
func (r *Engine) CompileTags(
	ctx context.Context, b domain.BankRef, f domain.TagFilter, minSimilarity float64,
) (domain.TagPredicate, error) {
	return r.tags.Compile(ctx, b, f, minSimilarity)
}

// retrieve runs arms concurrently, each over its own copy of q's Query. It returns each arm's list by name and one
// trace entry per arm that ran, in arm order; an arm that returns ErrSkip has neither.
func (r *Engine) retrieve(ctx context.Context, q *parsedQuery, arms []Arm) (
	map[string][]candidate, []ArmTrace, error,
) {
	results := make([][]candidate, len(arms))
	traces := make([]*ArmTrace, len(arms))
	g, gctx := errgroup.WithContext(ctx)
	for i, a := range arms {
		g.Go(func() error {
			t0 := r.clock.Now()
			hs, err := a.Retrieve(gctx, q.arm())
			if errors.Is(err, ErrSkip) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("recall: %s arm: %w", a.Name(), err)
			}
			cs := candidates(a.Name(), hs, q.Limit)
			results[i] = cs
			traces[i] = &ArmTrace{
				Arm: a.Name(), Candidates: len(cs), DurationMS: r.clock.Now().Sub(t0).Milliseconds(),
				TopIDs: topIDs(cs),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("recall: retrieve: %w", err)
	}
	lists := make(map[string][]candidate, len(arms))
	var out []ArmTrace
	for i, a := range arms {
		if traces[i] != nil {
			lists[a.Name()] = results[i]
			out = append(out, *traces[i])
		}
	}
	return lists, out, nil
}

// candidates turns an arm's hits (best first) into candidates carrying their 1-based rank and raw score,
// keeping at most limit (<= 0 = all) and dropping repeated ids.
func candidates(arm string, hs []Hit, limit int) []candidate {
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

// rank keeps the top reranker_max_candidates of fused (strategy boosts decide that cut), hydrates them
// (units archived since retrieval are dropped), scores them and applies the result floors. It also returns
// how many the cross-encoder scored.
func (r *Engine) rank(ctx context.Context, q *parsedQuery, fused []candidate, set settings) (
	[]candidate, int, error,
) {
	top := trimBoosted(fused, set.rerankMax, set.rrfK, set.boosts)
	hydrated, err := r.hydrate(ctx, q, top)
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

// hydrate attaches each candidate's unit, keeping fused order. It is the engine's re-check of every arm's hits: it
// keeps only live units of q's bank (GetUnits) that pass q's fact types, tag predicate and seq bound, so it drops
// ids that no longer resolve, units rewritten outside the seq bound since the arm saw them (a refresh's snapshot
// stays at or below S), and anything an arm returned that the query excludes. For the built-in arms, which filter
// natively, it removes only what changed since retrieval.
func (r *Engine) hydrate(ctx context.Context, q *parsedQuery, cs []candidate) ([]candidate, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	ids := make([]domain.UnitID, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	units, err := r.read.GetUnits(ctx, q.Bank, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.UnitID]*domain.Unit, len(units))
	for i := range units {
		if admits(q, units[i]) {
			byID[units[i].ID] = &units[i]
		}
	}
	out := make([]candidate, 0, len(cs))
	for _, c := range cs {
		if u, ok := byID[c.ID]; ok {
			c.Unit = u
			out = append(out, c)
		}
	}
	return out, nil
}

// admits reports whether u passes q's bank, fact types, tag predicate and seq bound.
func admits(q *parsedQuery, u domain.Unit) bool {
	return u.Bank == q.Bank && (len(q.Types) == 0 || slices.Contains(q.Types, u.Type)) &&
		q.Predicate.Eval(u.Tags) && q.Seq.Contains(u.Seq)
}

// staleScopes returns the canonical keys of the packed observations' scopes that still hold unconsolidated
// facts (Reader.StaleScopes, one call over the distinct scopes).
func (r *Engine) staleScopes(ctx context.Context, b domain.BankRef, packed []candidate) (map[string]bool, error) {
	var scopes []domain.ScopeKey
	seen := map[string]bool{}
	for _, c := range packed {
		if c.Unit == nil || c.Unit.Type != domain.FactTypeObservation {
			continue
		}
		s := domain.NewScopeKey(b, c.Unit.Tags)
		if k := s.String(); !seen[k] {
			seen[k] = true
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 0 {
		return nil, nil
	}
	stale, err := r.read.StaleScopes(ctx, b, scopes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(stale))
	for _, s := range stale {
		out[s.String()] = true
	}
	return out, nil
}

// result builds the Result from the packed candidates, the enrichment and the stale scope keys.
func result(packed []candidate, e enrichment, stale map[string]bool) Result {
	out := Result{
		Results: make([]Memory, 0, len(packed)), SourceFacts: e.SourceFacts,
		SourceFactsTruncated: e.SourceFactsTruncated, Chunks: e.Chunks, Entities: e.Entities,
	}
	for _, c := range packed {
		m := Memory{Unit: *c.Unit, Scores: scoresOf(c)}
		if c.Unit.Type == domain.FactTypeObservation {
			m.Stale = stale[domain.NewScopeKey(c.Unit.Bank, c.Unit.Tags).String()]
		}
		out.Results = append(out.Results, m)
	}
	return out
}

// scoresOf returns a candidate's wire scores.
func scoresOf(c candidate) Scores {
	s := Scores{Final: c.Final, Reranker: c.Reranker, Arms: make(map[string]float64, len(c.Arms))}
	for arm, a := range c.Arms {
		s.Arms[arm] = a.Score
	}
	return s
}

// tokensOf sums the packed candidates' token counts.
func tokensOf(cs []candidate) int {
	n := 0
	for _, c := range cs {
		n += c.Tokens
	}
	return n
}

// topIDs returns the first traceTopIDs ids of an arm's list.
func topIDs(cs []candidate) []domain.UnitID {
	out := make([]domain.UnitID, 0, min(len(cs), traceTopIDs))
	for _, c := range cs[:min(len(cs), traceTopIDs)] {
		out = append(out, c.ID)
	}
	return out
}
