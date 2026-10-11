package arms_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	"memd/internal/recall/armtest"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
)

// TestContract runs the arm contract suite for each built-in arm on its own database.
func TestContract(t *testing.T) {
	for _, name := range []string{"vector", "text", "graph", "temporal"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			armtest.Run(t, func(t *testing.T) armtest.Fixture {
				pg := pgtest.NewStore(t)
				for _, a := range builtins(pg.Read(), pg.Graph()) {
					if a.Name() == name {
						return armtest.Fixture{DB: pg, Arm: a}
					}
				}
				t.Fatalf("no built-in arm %q", name)
				return armtest.Fixture{}
			})
		})
	}
}

// TestRecallArmsAndTrace: without a window the temporal arm skips (no trace entry); the graph arm reaches a unit
// only linked to a vector seed over a semantic link, never over a temporal one; recall.arms and the request's arms
// select the arms that run, in registration order.
func TestRecallArmsAndTrace(t *testing.T) {
	e := newEnv(t)
	seed := fact("seed", "rocket launch schedule")
	seed.OccurredStart = at(-3)
	e.add(t, seed, fact("hop", "unrelated words entirely"), fact("tnext", "zzz qqq"))
	e.link(t, "seed", "hop", graph.KindSemantic, 0.8)
	e.link(t, "seed", "tnext", graph.KindTemporal, 1)
	res := e.recall(t, recall.Request{Query: "rocket launch schedule", Trace: true})
	if got := sortedIDs(res); !slices.Equal(got, []string{"hop", "seed"}) {
		t.Fatalf("results = %v, want the graph hop too", got)
	}
	if got := armNames(res); !slices.Equal(got, []string{"vector", "text", "graph"}) {
		t.Fatalf("arms = %v", got)
	}
	if g := res.Trace.Arms[2]; g.Candidates != 1 || !slices.Equal(g.TopIDs, []domain.UnitID{uid("hop")}) {
		t.Fatalf("graph trace = %+v", g)
	}
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -7), End: testNow}
	if got := armNames(e.recall(t, recall.Request{Query: "rocket", Window: w, Trace: true})); len(got) != 4 {
		t.Fatalf("arms with window = %v", got)
	}
	only := &domain.BankConfig{Recall: &domain.RecallConfig{Arms: []string{"text"}}}
	if got := armNames(e.recall(t, recall.Request{Query: "rocket", Overrides: only, Trace: true})); !slices.Equal(got,
		[]string{"text"}) {
		t.Fatalf("recall.arms = [text]: %v", got)
	}
	cleared := &domain.BankConfig{Recall: &domain.RecallConfig{Arms: []string{}}}
	if got := armNames(e.recall(t, recall.Request{Query: "rocket", Overrides: cleared, Trace: true})); len(got) != 3 {
		t.Fatalf("cleared recall.arms: %v", got)
	}
	res = e.recall(t, recall.Request{
		Query: "rocket launch schedule", Window: w, Arms: []string{"graph", "vector"}, Overrides: only, Trace: true,
	})
	if got := armNames(res); !slices.Equal(got, []string{"vector", "graph"}) {
		t.Fatalf("request arms [graph vector]: ran %v, want [vector graph]", got)
	}
	if got := sortedIDs(res); !slices.Equal(got, []string{"hop", "seed"}) {
		t.Fatalf("request arms [graph vector]: results = %v", got)
	}
	_, err := e.rc.Recall(context.Background(), recall.Request{Bank: testBank, Query: "x", Arms: []string{"semantic"}})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("unknown arm: err = %v, want INVALID_ARGUMENT", err)
	}
}

// TestRecallTagGroups: the compiled tag filter reaches every arm's search, with each match mode and group kind.
func TestRecallTagGroups(t *testing.T) {
	e := newEnv(t)
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
		{"empty leaf is no filter", domain.TagFilter{Groups: []domain.TagGroup{
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
			res := e.recall(t, recall.Request{Query: "note", Tags: tt.f})
			if got := sortedIDs(res); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	_, err := e.rc.Recall(context.Background(), recall.Request{Bank: testBank, Query: "note", Tags: domain.TagFilter{
		Tags: []string{"a"}, Groups: []domain.TagGroup{leaf(strict, "b")},
	}})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("tags and groups: err = %v, want INVALID_ARGUMENT", err)
	}
}

// TestRecallFuzzyTags: a fuzzy leaf resolves against the bank's tags, so a typo matches the real tag and the
// filter still excludes other tags.
func TestRecallFuzzyTags(t *testing.T) {
	e := newEnv(t)
	e.add(t, fact("eng", "rocket launch notes", "engineering"), fact("mkt", "rocket launch notes", "marketing"))
	g := leaf(domain.TagsMatchAnyStrict, "enginering")
	g.Leaf.Resolve = domain.TagResolveFuzzy
	res := e.recall(t, recall.Request{Query: "rocket launch notes", Tags: domain.TagFilter{Groups: []domain.TagGroup{g}}})
	if got := ids(res); !slices.Equal(got, []string{"eng"}) {
		t.Fatalf("fuzzy 'enginering' = %v, want [eng]", got)
	}
}

// TestRecallMinScoresArm: min_scores.arms reaches the vector and text searches by name; the graph arm's seed search
// keeps its own 0.3.
func TestRecallMinScoresArm(t *testing.T) {
	e := newEnv(t)
	e.add(t, fact("pie", "apple pie recipe"), fact("zebra", "zebra stripes"))
	s := &spy{Search: e.store.Read()}
	e.rc = e.engine(s)
	res := e.recall(t, recall.Request{Query: "apple pie recipe", Trace: true, MinScores: domain.MinScores{
		Arms: map[string]float64{"vector": 0.9, "text": 0.01},
	}})
	if got := ids(res); !slices.Equal(got, []string{"pie"}) {
		t.Fatalf("results = %v, want [pie]", got)
	}
	var floors []float64
	for _, q := range s.vectors {
		floors = append(floors, q.MinSimilarity)
	}
	slices.Sort(floors)
	if !slices.Equal(floors, []float64{0.3, 0.9}) {
		t.Fatalf("vector floors = %v, want the graph seeds' 0.3 and the request's 0.9", floors)
	}
	if len(s.texts) != 1 || s.texts[0].MinScore != 0.01 {
		t.Fatalf("text queries = %+v", s.texts)
	}
	for _, a := range res.Trace.Arms {
		if a.Arm == "vector" && a.Candidates != 1 {
			t.Fatalf("vector arm returned %d candidates over the floor", a.Candidates)
		}
	}
}

// TestVectorDefaultFloor: without a min_scores entry the vector arm's floor is 0.3; an explicit 0 disables it.
func TestVectorDefaultFloor(t *testing.T) {
	e := newEnv(t)
	e.add(t, fact("pie", "apple pie recipe"))
	s := &spy{Search: e.store.Read()}
	d := e.deps
	d.Arms = []recall.Arm{arms.NewVector(s)}
	e.rc = recall.New(d)
	for _, tt := range []struct {
		name string
		m    map[string]float64
		want float64
	}{
		{"unset = 0.3", nil, 0.3},
		{"explicit 0 disables", map[string]float64{"vector": 0}, 0},
		{"explicit value", map[string]float64{"vector": 0.7}, 0.7},
	} {
		s.vectors = nil
		e.recall(t, recall.Request{Query: "apple pie", MinScores: domain.MinScores{Arms: tt.m}})
		if len(s.vectors) != 1 || s.vectors[0].MinSimilarity != tt.want {
			t.Fatalf("%s: vector queries = %+v", tt.name, s.vectors)
		}
	}
}

// TestTemporalRelevanceFilter: the temporal arm passes the query embedding and the 0.1 floor, so SearchTemporal
// drops an in-window unit with no embedding (the filter is on) and keeps the relevant one.
func TestTemporalRelevanceFilter(t *testing.T) {
	e := newEnv(t)
	rel := fact("rel", "rocket launch")
	rel.OccurredStart = at(-1)
	e.add(t, rel)
	e.tx(t, func(ctx context.Context, tx store.Tx, seq int64) error {
		noEmb := fact("noemb", "rocket launch")
		noEmb.OccurredStart, noEmb.Seq, noEmb.Tags = at(-1), seq, []string{}
		return tx.Units().Insert(ctx, []domain.Unit{noEmb})
	})
	s := &spy{Search: e.store.Read()}
	d := e.deps
	d.Arms = []recall.Arm{arms.NewTemporal(s)}
	e.rc = recall.New(d)
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -7), End: testNow}
	if got := ids(e.recall(t, recall.Request{Query: "rocket launch", Window: w})); !slices.Equal(got, []string{"rel"}) {
		t.Fatalf("temporal arm = %v, want only rel", got)
	}
	if len(s.temporals) != 1 || s.temporals[0].MinSimilarity != 0.1 || len(s.temporals[0].Embedding) == 0 {
		t.Fatalf("temporal query = %+v, want the query embedding and MinSimilarity 0.1", s.temporals)
	}
}

// TestRecallNoCueNoTimeParse: a query without a time cue makes no TIME_PARSE call and has no window, so the
// temporal arm skips.
func TestRecallNoCueNoTimeParse(t *testing.T) {
	e := newEnv(t)
	e.add(t, fact("u1", "alice likes green tea"))
	res := e.recall(t, recall.Request{Query: "what does alice like to drink", Trace: true})
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

// TestRecallExplicitWindowNoTimeParse: an explicit window is used as given, with no time analysis.
func TestRecallExplicitWindowNoTimeParse(t *testing.T) {
	e := newEnv(t)
	// The unit shares "what happened" with both queries: the temporal arm keeps only in-window units with a
	// cosine of at least 0.1 to the query.
	u := fact("u1", "what happened on the trip to the mountains")
	u.OccurredStart = at(-10)
	e.add(t, u)
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -30), End: testNow}
	for _, q := range []string{"what happened last spring", "what happened around the thing back when"} {
		res := e.recall(t, recall.Request{Query: q, Window: w, Trace: true})
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
