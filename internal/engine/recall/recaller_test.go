package recall

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"sync"
	"testing"
)

// spyReader records the arm floors each search was given.
type spyReader struct {
	port.Reader
	mu      sync.Mutex
	vectors []port.VectorQuery
	texts   []port.TextQuery
}

func (s *spyReader) SearchVector(ctx context.Context, q port.VectorQuery) ([]port.Hit, error) {
	s.mu.Lock()
	s.vectors = append(s.vectors, q)
	s.mu.Unlock()
	return s.Reader.SearchVector(ctx, q)
}

func (s *spyReader) SearchText(ctx context.Context, q port.TextQuery) ([]port.Hit, error) {
	s.mu.Lock()
	s.texts = append(s.texts, q)
	s.mu.Unlock()
	return s.Reader.SearchText(ctx, q)
}

// leaf builds a leaf tag group.
func leaf(m domain.TagsMatch, tags ...string) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: tags, Match: m}}
}

func group(kind domain.TagGroupKind, gs ...domain.TagGroup) domain.TagGroup {
	if kind == domain.TagGroupNoneOf {
		return domain.TagGroup{Kind: kind, Not: &gs[0]}
	}
	return domain.TagGroup{Kind: kind, Groups: gs}
}

func TestRecallTagGroups(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("u1", "note one", "a"), fact("u2", "note two", "a", "b"), fact("u3", "note three", "b"),
		fact("u4", "note four"), fact("u5", "note five", "c"))
	strict, lenient := domain.TagsMatchAnyStrict, domain.TagsMatchAny
	tests := []struct {
		name string
		f    domain.TagFilter
		want []string
	}{
		{"no filter", domain.TagFilter{}, []string{"u1", "u2", "u3", "u4", "u5"}},
		{"flat default any admits untagged", domain.TagFilter{Tags: []string{"A"}}, []string{"u1", "u2", "u4"}},
		{"flat any_strict", domain.TagFilter{Tags: []string{"a"}, Match: strict}, []string{"u1", "u2"}},
		{"flat exact", domain.TagFilter{Tags: []string{"a"}, Match: domain.TagsMatchExact}, []string{"u1"}},
		{"all_of strict leaves", domain.TagFilter{Groups: []domain.TagGroup{
			group(domain.TagGroupAllOf, leaf(strict, "a"), leaf(strict, "b")),
		}}, []string{"u2"}},
		{"any_of strict leaves", domain.TagFilter{Groups: []domain.TagGroup{
			group(domain.TagGroupAnyOf, leaf(strict, "a"), leaf(strict, "b")),
		}}, []string{"u1", "u2", "u3"}},
		{"none_of strict", domain.TagFilter{Groups: []domain.TagGroup{
			group(domain.TagGroupNoneOf, leaf(strict, "a")),
		}}, []string{"u3", "u4", "u5"}},
		{"none_of lenient excludes untagged", domain.TagFilter{Groups: []domain.TagGroup{
			group(domain.TagGroupNoneOf, leaf(lenient, "a")),
		}}, []string{"u3", "u5"}},
		{"leaf default is any_strict", domain.TagFilter{Groups: []domain.TagGroup{leaf("", "c")}}, []string{"u5"}},
		{
			"leaf all lenient",
			domain.TagFilter{Groups: []domain.TagGroup{leaf(domain.TagsMatchAll, "a", "b")}},
			[]string{"u2", "u4"},
		},
		{
			"leaf all_strict",
			domain.TagFilter{Groups: []domain.TagGroup{leaf(domain.TagsMatchAllStrict, "a", "b")}},
			[]string{"u2"},
		},
		{"nested none_of(any_of)", domain.TagFilter{Groups: []domain.TagGroup{group(domain.TagGroupNoneOf,
			group(domain.TagGroupAnyOf, leaf(strict, "a"), leaf(strict, "c")))}}, []string{"u3", "u4"}},
		{"empty leaf is no filter (domain.MatchGroup, decision 903)", domain.TagFilter{Groups: []domain.TagGroup{
			leaf(strict),
		}}, []string{"u1", "u2", "u3", "u4", "u5"}},
		{"empty exact leaf selects untagged", domain.TagFilter{Groups: []domain.TagGroup{
			leaf(domain.TagsMatchExact),
		}}, []string{"u4"}},
		{"top-level groups AND-ed", domain.TagFilter{Groups: []domain.TagGroup{
			leaf(strict, "b"),
			group(domain.TagGroupNoneOf, leaf(strict, "a")),
		}}, []string{"u3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := e.recall(t, RecallRequest{Query: "note", Tags: tt.f})
			if got := sortedIDs(res); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	_, err := e.rc.Recall(context.Background(), RecallRequest{Bank: testBank, Query: "note", Tags: domain.TagFilter{
		Tags: []string{"a"}, Groups: []domain.TagGroup{leaf(strict, "b")},
	}})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("tags and groups: err = %v, want INVALID_ARGUMENT", err)
	}
}

func TestRecallMinScoresArm(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("pie", "apple pie recipe"), fact("zebra", "zebra stripes"))
	spy := &spyReader{Reader: e.store.Read()}
	e.rc = NewRecaller(RecallerDeps{
		Reader: spy, Config: e.rc.(*recaller).d.Config, Embedder: e.emb, Router: e.rc.(*recaller).d.Router,
		Clock: e.clock, Count: wordCounter{},
	})
	res := e.recall(t, RecallRequest{Query: "apple pie recipe", Trace: true, MinScores: domain.MinScores{
		Semantic: ptr(0.9), Keyword: ptr(0.01),
	}})
	if got := ids(res); !slices.Equal(got, []string{"pie"}) {
		t.Fatalf("results = %v, want [pie]", got)
	}
	var floors []float64
	for _, q := range spy.vectors {
		floors = append(floors, q.MinSimilarity)
	}
	slices.Sort(floors)
	// The semantic arm gets the request floor; the graph arm's seed search keeps its own 0.3.
	if !slices.Equal(floors, []float64{graphSeedMinSimilarity, 0.9}) {
		t.Fatalf("vector floors = %v", floors)
	}
	if len(spy.texts) != 1 || spy.texts[0].MinScore != 0.01 {
		t.Fatalf("text queries = %+v", spy.texts)
	}
	for _, a := range res.Trace.Arms {
		if a.Arm == ArmSemantic && a.Candidates != 1 {
			t.Fatalf("semantic arm returned %d candidates over the floor", a.Candidates)
		}
	}
}

func TestRecallMinScoresResult(t *testing.T) {
	e := newEnv(t, envOpts{rerank: true, arms: []Retriever{arm(ArmSemantic, "hi", "mid", "lo")}})
	e.add(t, fact("hi", "doc hi"), fact("mid", "doc mid"), fact("lo", "doc lo"))
	e.rr.SetFunc(func(_, doc string) float32 {
		return map[string]float32{"doc hi": 0.9, "doc mid": 0.6, "doc lo": 0.3}[doc]
	})
	noBoost := alphas(0, 0, 0)
	res := e.recall(t, RecallRequest{Query: "doc", Overrides: noBoost, MinScores: domain.MinScores{Reranker: ptr(0.5)}})
	if got := ids(res); !slices.Equal(got, []string{"hi", "mid"}) {
		t.Fatalf("reranker floor: %v", got)
	}
	res = e.recall(t, RecallRequest{Query: "doc", Overrides: noBoost, MinScores: domain.MinScores{Final: ptr(0.7)}})
	if got := ids(res); !slices.Equal(got, []string{"hi"}) {
		t.Fatalf("final floor: %v", got)
	}
	if r := res.Results[0].Scores.Reranker; r == nil || !near(*r, float64(float32(0.9))) {
		t.Fatalf("reranker score = %v", r)
	}
}

func TestScoreGate(t *testing.T) {
	g := NewScoreGate()
	m := domain.MinScores{Semantic: ptr(0.4), Reranker: ptr(0.5), Final: ptr(0.2)}
	if f, ok := g.ArmFloor(ArmSemantic, m); !ok || f != 0.4 {
		t.Fatalf("semantic floor = %v %v", f, ok)
	}
	if _, ok := g.ArmFloor(ArmKeyword, m); ok {
		t.Fatal("keyword floor set")
	}
	if _, ok := g.ArmFloor(ArmGraph, m); ok {
		t.Fatal("graph has no floor")
	}
	tests := []struct {
		c    Candidate
		keep bool
	}{
		{Candidate{Reranker: ptr(0.6), Final: 0.3}, true},
		{Candidate{Reranker: ptr(0.4), Final: 0.9}, false},
		{Candidate{Reranker: nil, Final: 0.3}, true}, // not reranked: the reranker floor doesn't apply
		{Candidate{Reranker: ptr(0.9), Final: 0.1}, false},
	}
	for i, tt := range tests {
		if got := g.Keep(tt.c, m); got != tt.keep {
			t.Errorf("case %d: Keep = %v, want %v", i, got, tt.keep)
		}
	}
}

func TestRecallPreferObservationsBackfill(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic, "f1", "o1", "f2", "f3", "f4")}})
	e.add(t, fact("f1", "fact one"), fact("f2", "fact two"), fact("f3", "fact three"), fact("f4", "fact four"))
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs one"})
	e.cite(t, "o1", "f1", "f2")
	base := RecallRequest{Query: "fact", MaxTokens: 6, Overrides: alphas(0, 0, 0)}
	if got := ids(e.recall(t, base)); !slices.Equal(got, []string{"f1", "o1", "f2"}) {
		t.Fatalf("without prefer: %v", got)
	}
	base.PreferObservations = true
	if got := ids(e.recall(t, base)); !slices.Equal(got, []string{"o1", "f3", "f4"}) {
		t.Fatalf("with prefer: %v, want [o1 f3 f4]", got)
	}
}

func TestRecallNoCueNoTimeParse(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("u1", "alice likes green tea"))
	res := e.recall(t, RecallRequest{Query: "what does alice like to drink", Trace: true})
	if len(e.llm.Calls()) != 0 {
		t.Fatalf("LLM calls = %d, want 0", len(e.llm.Calls()))
	}
	if res.Trace.Window != nil || len(res.Trace.Arms) != 3 {
		t.Fatalf("window = %v, arms = %+v; want no window and no temporal arm", res.Trace.Window, res.Trace.Arms)
	}
	if got := ids(res); !slices.Equal(got, []string{"u1"}) {
		t.Fatalf("results = %v", got)
	}
}

func TestRecallExplicitWindowNoTimeParse(t *testing.T) {
	e := newEnv(t, envOpts{})
	// The unit shares "what happened" with both queries: the temporal arm keeps only in-window units with a
	// cosine of at least 0.1 to the query.
	u := fact("u1", "what happened on the trip to the mountains")
	u.OccurredStart = at(-10)
	e.add(t, u)
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -30), End: testNow}
	for _, q := range []string{"what happened last spring", "what happened around the thing back when"} {
		res := e.recall(t, RecallRequest{Query: q, Window: w, Trace: true})
		if len(e.llm.Calls()) != 0 {
			t.Fatalf("%q: LLM calls = %d, want 0", q, len(e.llm.Calls()))
		}
		if res.Trace.Window == nil || *res.Trace.Window != *w || len(res.Trace.Arms) != 4 {
			t.Fatalf("%q: window = %v arms = %d", q, res.Trace.Window, len(res.Trace.Arms))
		}
		if !slices.Contains(ids(res), "u1") {
			t.Fatalf("%q: results = %v", q, ids(res))
		}
	}
}

func TestRecallRerankTopN(t *testing.T) {
	sem := arm(ArmSemantic, "u1", "u2", "u3", "u4", "u5")
	e := newEnv(t, envOpts{rerank: true, arms: []Retriever{sem}})
	e.add(t, fact("u1", "doc a"), fact("u2", "doc b"), fact("u3", "doc c"), fact("u4", "doc d"), fact("u5", "doc e"))
	e.rr.SetFunc(func(_, doc string) float32 { return map[string]float32{"doc c": 0.9}[doc] })
	cfg := recallCfg(domain.RecallConfig{RerankerMaxCandidates: ptr(3), RecencyAlpha: ptr(0.0)})
	res := e.recall(t, RecallRequest{Query: "doc", Overrides: cfg, Trace: true})
	calls := e.rr.Calls()
	if len(calls) != 1 || len(calls[0].Docs) != 3 || calls[0].Binding.Model != "rerank-1" {
		t.Fatalf("rerank calls = %+v", calls)
	}
	if got := ids(res); !slices.Equal(got, []string{"u3", "u1", "u2"}) {
		t.Fatalf("results = %v, want [u3 u1 u2]", got)
	}
	if res.Trace.Fused != 5 || res.Trace.Reranked != 3 || res.Trace.Packed != 3 {
		t.Fatalf("trace = %+v", res.Trace)
	}
}

func TestRecallRerankDisabledUsesRRF(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic, "u1", "u2", "u3")}}) // no RERANK binding
	e.add(t, fact("u1", "doc a"), fact("u2", "doc b"), fact("u3", "doc c"))
	res := e.recall(t, RecallRequest{Query: "doc", Overrides: alphas(0, 0, 0), Trace: true})
	if len(e.rr.Calls()) != 0 || res.Trace.Reranked != 0 {
		t.Fatalf("reranker called with rerank off")
	}
	want := []float64{1, 0.55, 0.1}
	for i, m := range res.Results {
		if m.Scores.Reranker != nil || !near(m.Scores.Final, want[i]) {
			t.Fatalf("result %d scores = %+v, want final %v", i, m.Scores, want[i])
		}
	}
}

func TestRecallArmsAndTrace(t *testing.T) {
	e := newEnv(t, envOpts{})
	seed := fact("seed", "rocket launch schedule")
	seed.OccurredStart = at(-3)
	e.add(t, seed, fact("hop", "unrelated words entirely"))
	e.link(t, "seed", "hop", 0.8)
	e.add(t, fact("tnext", "zzz qqq"))
	e.tx(t, func(ctx context.Context, tx port.Tx, _ int64) error {
		return tx.Links().Insert(ctx, []domain.Link{{
			Bank: testBank, From: uid("seed"), To: uid("tnext"), Kind: domain.LinkTemporal, Weight: 1,
		}})
	})
	res := e.recall(t, RecallRequest{Query: "rocket launch schedule", Trace: true})
	if got := sortedIDs(res); !slices.Equal(got, []string{"hop", "seed"}) {
		t.Fatalf("results = %v, want the graph hop too", got)
	}
	arms := func(r RecallResult) []string {
		var out []string
		for _, a := range r.Trace.Arms {
			out = append(out, a.Arm)
		}
		return out
	}
	if got := arms(res); !slices.Equal(got, []string{ArmSemantic, ArmKeyword, ArmGraph}) {
		t.Fatalf("arms = %v", got)
	}
	if g := res.Trace.Arms[2]; g.Candidates != 1 || !slices.Equal(g.TopIDs, uids("hop")) {
		t.Fatalf("graph trace = %+v", g)
	}
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -7), End: testNow}
	if got := arms(e.recall(t, RecallRequest{Query: "rocket", Window: w, Trace: true})); len(got) != 4 {
		t.Fatalf("arms with window = %v", got)
	}
	only := recallCfg(domain.RecallConfig{EnabledStrategies: []string{ArmKeyword}})
	if got := arms(e.recall(t, RecallRequest{Query: "rocket", Overrides: only, Trace: true})); !slices.Equal(got,
		[]string{ArmKeyword}) {
		t.Fatalf("enabled keyword only: %v", got)
	}
	cleared := recallCfg(domain.RecallConfig{EnabledStrategies: []string{}})
	if got := arms(e.recall(t, RecallRequest{Query: "rocket", Overrides: cleared, Trace: true})); len(got) != 3 {
		t.Fatalf("cleared enabled_strategies: %v", got)
	}
	if res.Trace.TokenBudget != DefaultMaxTokens || res.Trace.TokensUsed != 6 {
		t.Fatalf("trace tokens = %d / %d", res.Trace.TokensUsed, res.Trace.TokenBudget)
	}
}

func TestRecallIncludes(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic, "o1", "f2")}})
	e.addChunks(t, "d1", domain.Chunk{ID: cid("c2"), Index: 0, Text: "chunk of f2"})
	f2 := fact("f2", "other fact")
	f2.DocumentID, f2.ChunkID = "d1", cid("c2")
	e.add(t, fact("f1", "source fact"), f2)
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs"})
	e.cite(t, "o1", "f1")
	var alice port.EntityID
	e.tx(t, func(ctx context.Context, tx port.Tx, _ int64) error {
		var err error
		if alice, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: testBank, CanonicalName: "Alice"}); err != nil {
			return err
		}
		for _, u := range uids("f1", "f2") {
			if err := tx.Entities().Attach(ctx, u, []port.EntityID{alice}); err != nil {
				return err
			}
		}
		return nil
	})
	res := e.recall(t, RecallRequest{Query: "obs", Include: domain.RecallInclude{
		SourceFacts: ptr(true), Entities: ptr(true), Chunks: ptr(true),
	}})
	if _, ok := res.SourceFacts[uid("f1")]; !ok || res.SourceFactsTruncated {
		t.Fatalf("source facts = %v", res.SourceFacts)
	}
	got := res.Entities[alice]
	if got.CanonicalName != "Alice" || !slices.Equal(got.ObservationIDs, uids("o1")) {
		t.Fatalf("entities = %+v, want Alice (from f2) with observation o1 (citing f1)", res.Entities)
	}
	if c := res.Chunks[cid("c2")]; c.Text != "chunk of f2" || len(res.Chunks) != 1 {
		t.Fatalf("chunks = %+v, want f2's chunk", res.Chunks)
	}
}

func TestRecallUnknownBank(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, err := e.rc.Recall(context.Background(), RecallRequest{Bank: port.BankRef{Tenant: "t1", Bank: "nope"}, Query: "x"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want NOT_FOUND", err)
	}
	if len(e.emb.Calls()) != 0 {
		t.Fatal("embedded a query for an unknown bank")
	}
	_, err = e.rc.Recall(context.Background(), RecallRequest{Bank: port.BankRef{Tenant: "t1", Bank: "nope"}, Query: "  "})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("blank query: err = %v", err)
	}
}

func TestRecallFusionOrderGolden(t *testing.T) {
	sem := arm(ArmSemantic, "a", "b", "c")
	kw := arm(ArmKeyword, "b", "c", "d")
	gr := arm(ArmGraph, "d")
	e := newEnv(t, envOpts{arms: []Retriever{sem, kw, gr}})
	e.add(t, fact("a", "x a"), fact("b", "x b"), fact("c", "x c"), fact("d", "x d"))
	res := e.recall(t, RecallRequest{Query: "x", Overrides: alphas(0, 0, 0), Budget: domain.BudgetLow})
	if got := ids(res); !slices.Equal(got, []string{"b", "d", "c", "a"}) {
		t.Fatalf("order = %v, want [b d c a]", got)
	}
	if sem.seen.Limit != 100 {
		t.Fatalf("arm limit = %d, want 100 for BUDGET_LOW", sem.seen.Limit)
	}
	if s := res.Results[0].Scores; s.Semantic == nil || s.Keyword == nil || *s.Keyword != 1 {
		t.Fatalf("scores of b = %+v", s)
	}
}

func TestRecallCueFallbackCallsTimeParse(t *testing.T) {
	// Positive control for the no-cue test: a cue the rules can't resolve reaches TIME_PARSE once; the
	// unscripted call fails and recall degrades to no window instead of failing.
	e := newEnv(t, envOpts{})
	e.add(t, fact("u1", "family dinner around the holidays"))
	res := e.recall(t, RecallRequest{Query: "what did we do around the holidays?", Trace: true})
	if n := len(e.llm.CallsFor(domain.ModelSlotTimeParse, "time_parse_system")); n != 1 {
		t.Fatalf("TIME_PARSE calls = %d, want 1 (all calls: %d)", n, len(e.llm.Calls()))
	}
	if res.Trace.Window != nil || !slices.Contains(ids(res), "u1") {
		t.Fatalf("window = %v results = %v", res.Trace.Window, ids(res))
	}
}

func TestRecallSeqSnapshot(t *testing.T) {
	sem := arm(ArmSemantic, "old", "late")
	e := newEnv(t, envOpts{arms: []Retriever{sem}})
	e.add(t, fact("old", "early fact"))
	snapshot, err := e.store.Read().BankSeq(context.Background(), testBank)
	if err != nil {
		t.Fatal(err)
	}
	e.add(t, fact("late", "late fact")) // written after the snapshot; the stub arm still returns it
	res := e.recall(t, RecallRequest{Query: "fact", Seq: port.SeqUpTo(0, snapshot)})
	if got := ids(res); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("snapshot results = %v, want [old]", got)
	}
	if got := sortedIDs(e.recall(t, RecallRequest{Query: "fact"})); !slices.Equal(got, []string{"late", "old"}) {
		t.Fatalf("unbounded results = %v", got)
	}
}

func TestRecallSemanticDefaultFloor(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("pie", "apple pie recipe"))
	spy := &spyReader{Reader: e.store.Read()}
	e.rc = NewRecaller(RecallerDeps{
		Reader: spy, Config: e.rc.(*recaller).d.Config, Embedder: e.emb, Router: e.rc.(*recaller).d.Router,
		Clock: e.clock, Count: wordCounter{}, Retrievers: []Retriever{NewSemantic(spy)},
	})
	for _, tt := range []struct {
		name string
		m    domain.MinScores
		want float64
	}{
		{"unset = 0.3", domain.MinScores{}, 0.3},
		{"explicit 0 disables", domain.MinScores{Semantic: ptr(0.0)}, 0},
		{"explicit value", domain.MinScores{Semantic: ptr(0.7)}, 0.7},
	} {
		spy.vectors = nil
		e.recall(t, RecallRequest{Query: "apple pie", MinScores: tt.m})
		if len(spy.vectors) != 1 || spy.vectors[0].MinSimilarity != tt.want {
			t.Fatalf("%s: vector queries = %+v", tt.name, spy.vectors)
		}
	}
}

// failingArm is a Retriever that always fails.
type failingArm struct{}

func (failingArm) Name() string { return ArmKeyword }

func (failingArm) Retrieve(context.Context, *ParsedQuery) ([]Candidate, error) {
	return nil, domain.ErrUnavailable
}

func TestRecallFailingArmFailsRecall(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic, "u1"), failingArm{}}})
	e.add(t, fact("u1", "doc"))
	_, err := e.rc.Recall(context.Background(), RecallRequest{Bank: testBank, Query: "doc"})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want the arm's UNAVAILABLE", err)
	}
}

func TestRecallLongQueryTruncated(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic)}})
	long := strings.TrimSpace(strings.Repeat("word ", 600))
	e.recall(t, RecallRequest{Query: long})
	calls := e.emb.Calls()
	if len(calls) != 1 || len(strings.Fields(calls[0].Texts[0])) != MaxQueryTokens {
		t.Fatalf("embedded query has %d words, want %d", len(strings.Fields(calls[0].Texts[0])), MaxQueryTokens)
	}
}
