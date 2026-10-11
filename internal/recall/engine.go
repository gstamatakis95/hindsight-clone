package recall

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"memd/internal/timex"
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
	arms     []retriever
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
	// DB serves the searches, hydration, includes, fuzzy tags, stale scopes and page listings through DB.Read, and
	// whole pages in a short read-only transaction.
	DB store.DB
	// Graph serves the graph arm's expansion (postgres.DB.Graph in production).
	Graph graph.Reader
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

// New returns the Engine over d with the four standard arms.
func New(d Deps) *Engine { return newEngine(d, d.DB.Read(), d.Tokens, nil) }

// newEngine is New with the reader, the token counter and the arms (nil = semantic, keyword, graph, temporal over
// read) chosen by the caller; tests spy on reads, count words and stub arms.
func newEngine(d Deps, read store.Reader, count tokenCounter, arms []retriever) *Engine {
	if arms == nil {
		arms = []retriever{newSemantic(read), newKeyword(read), newGraph(read, d.Graph), newTemporal(read)}
	}
	return &Engine{
		db: d.DB, read: read, config: d.Config, embedder: d.Embedder, times: d.Times, clock: d.Clock, count: count,
		arms: arms, tags: newTagCompiler(read), fuser: newRRF(), scorer: newScorer(d.Reranker, d.Router, d.Clock),
		gate: newScoreGate(), packer: newPacker(count), prefer: newObservationPreference(),
		includer: newIncluder(read, count),
	}
}

// Recall resolves the effective config (unknown bank = domain.ErrNotFound), parses, retrieves over the enabled
// arms concurrently, fuses, scores, gates, packs and enriches. It is read-only and opens a short read transaction
// only to resolve config. A failing arm, embedder or reranker fails the call; a recall that finds nothing is not a
// failure.
func (r *Engine) Recall(ctx context.Context, req Request) (Result, error) {
	start := r.clock.Now()
	if strings.TrimSpace(req.Query) == "" {
		return Result{}, fmt.Errorf("%w: recall: query is empty", domain.ErrInvalidArgument)
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
	lists, arms, err := r.retrieve(ctx, q, set)
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

// retrieve runs the enabled arms concurrently. The temporal arm runs only when the query has a window. It
// returns each arm's list by name and one trace entry per arm that ran, in arm order.
func (r *Engine) retrieve(ctx context.Context, q *parsedQuery, set settings) (
	map[string][]candidate, []ArmTrace, error,
) {
	var run []retriever
	for _, a := range r.arms {
		if set.enabled[a.Name()] && (a.Name() != ArmTemporal || q.Window != nil) {
			run = append(run, a)
		}
	}
	results := make([][]candidate, len(run))
	traces := make([]ArmTrace, len(run))
	g, gctx := errgroup.WithContext(ctx)
	for i, a := range run {
		g.Go(func() error {
			t0 := r.clock.Now()
			cs, err := a.Retrieve(gctx, q)
			if err != nil {
				return fmt.Errorf("recall: %s arm: %w", a.Name(), err)
			}
			results[i] = cs
			traces[i] = ArmTrace{
				Arm: a.Name(), Candidates: len(cs), DurationMS: r.clock.Now().Sub(t0).Milliseconds(),
				TopIDs: topIDs(cs),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("recall: retrieve: %w", err)
	}
	lists := make(map[string][]candidate, len(run))
	for i, a := range run {
		lists[a.Name()] = results[i]
	}
	return lists, traces, nil
}

// rank keeps the top reranker_max_candidates of fused (strategy boosts decide that cut), hydrates them
// (units archived since retrieval are dropped), scores them and applies the result floors. It also returns
// how many the cross-encoder scored.
func (r *Engine) rank(ctx context.Context, q *parsedQuery, fused []candidate, set settings) (
	[]candidate, int, error,
) {
	top := trimBoosted(fused, set.rerankMax, set.rrfK, set.boosts)
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
func (r *Engine) hydrate(ctx context.Context, b domain.BankRef, seq domain.SeqRange, cs []candidate) (
	[]candidate, error,
) {
	if len(cs) == 0 {
		return nil, nil
	}
	ids := make([]domain.UnitID, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	units, err := r.read.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.UnitID]*domain.Unit, len(units))
	for i := range units {
		if seq.Contains(units[i].Seq) {
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
