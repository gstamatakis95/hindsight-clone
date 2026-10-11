package recall

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
)

func TestRecallMinScoresResult(t *testing.T) {
	e := newEnv(t, envOpts{rerank: true, arms: []Arm{arm(armVector, "hi", "mid", "lo")}})
	e.add(t, fact("hi", "doc hi"), fact("mid", "doc mid"), fact("lo", "doc lo"))
	e.rr.SetFunc(func(_, doc string) float32 {
		return map[string]float32{"doc hi": 0.9, "doc mid": 0.6, "doc lo": 0.3}[doc]
	})
	noBoost := alphas(0, 0, 0)
	res := e.recall(t, Request{Query: "doc", Overrides: noBoost, MinScores: domain.MinScores{Reranker: ptr(0.5)}})
	if got := ids(res); !slices.Equal(got, []string{"hi", "mid"}) {
		t.Fatalf("reranker floor: %v", got)
	}
	res = e.recall(t, Request{Query: "doc", Overrides: noBoost, MinScores: domain.MinScores{Final: ptr(0.7)}})
	if got := ids(res); !slices.Equal(got, []string{"hi"}) {
		t.Fatalf("final floor: %v", got)
	}
	if r := res.Results[0].Scores.Reranker; r == nil || !near(*r, float64(float32(0.9))) {
		t.Fatalf("reranker score = %v", r)
	}
}

func TestScoreGate(t *testing.T) {
	g := newScoreGate()
	m := domain.MinScores{Reranker: ptr(0.5), Final: ptr(0.2)}
	tests := []struct {
		c    candidate
		keep bool
	}{
		{candidate{Reranker: ptr(0.6), Final: 0.3}, true},
		{candidate{Reranker: ptr(0.4), Final: 0.9}, false},
		{candidate{Reranker: nil, Final: 0.3}, true}, // not reranked: the reranker floor doesn't apply
		{candidate{Reranker: ptr(0.9), Final: 0.1}, false},
	}
	for i, tt := range tests {
		if got := g.Keep(tt.c, m); got != tt.keep {
			t.Errorf("case %d: Keep = %v, want %v", i, got, tt.keep)
		}
	}
}

func TestRecallPreferObservationsBackfill(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "f1", "o1", "f2", "f3", "f4")}})
	e.add(t, fact("f1", "fact one"), fact("f2", "fact two"), fact("f3", "fact three"), fact("f4", "fact four"))
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs one"})
	e.cite(t, "o1", "f1", "f2")
	base := Request{Query: "fact", MaxTokens: 6, Overrides: alphas(0, 0, 0)}
	if got := ids(e.recall(t, base)); !slices.Equal(got, []string{"f1", "o1", "f2"}) {
		t.Fatalf("without prefer: %v", got)
	}
	base.PreferObservations = true
	if got := ids(e.recall(t, base)); !slices.Equal(got, []string{"o1", "f3", "f4"}) {
		t.Fatalf("with prefer: %v, want [o1 f3 f4]", got)
	}
}

func TestRecallRerankTopN(t *testing.T) {
	sem := arm(armVector, "u1", "u2", "u3", "u4", "u5")
	e := newEnv(t, envOpts{rerank: true, arms: []Arm{sem}})
	e.add(t, fact("u1", "doc a"), fact("u2", "doc b"), fact("u3", "doc c"), fact("u4", "doc d"), fact("u5", "doc e"))
	e.rr.SetFunc(func(_, doc string) float32 { return map[string]float32{"doc c": 0.9}[doc] })
	cfg := recallCfg(domain.RecallConfig{RerankerMaxCandidates: ptr(3), RecencyAlpha: ptr(0.0)})
	res := e.recall(t, Request{Query: "doc", Overrides: cfg, Trace: true})
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
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "u1", "u2", "u3")}}) // no RERANK binding
	e.add(t, fact("u1", "doc a"), fact("u2", "doc b"), fact("u3", "doc c"))
	res := e.recall(t, Request{Query: "doc", Overrides: alphas(0, 0, 0), Trace: true})
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

func TestRecallIncludes(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "o1", "f2")}})
	e.addChunks(t, "d1", domain.Chunk{ID: cid("c2"), Index: 0, Text: "chunk of f2"})
	f2 := fact("f2", "other fact")
	f2.DocumentID, f2.ChunkID = "d1", cid("c2")
	e.add(t, fact("f1", "source fact"), f2)
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs"})
	e.cite(t, "o1", "f1")
	var alice domain.EntityID
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		var err error
		if alice, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: testBank, CanonicalName: "Alice"}); err != nil {
			return err
		}
		for _, u := range uids("f1", "f2") {
			if err := tx.Entities().Attach(ctx, u, []domain.EntityID{alice}); err != nil {
				return err
			}
		}
		return nil
	})
	res := e.recall(t, Request{Query: "obs", Include: domain.RecallInclude{
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
	nope := domain.BankRef{Tenant: "t1", Bank: "nope"}
	_, err := e.rc.Recall(context.Background(), Request{Bank: nope, Query: "x"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want NOT_FOUND", err)
	}
	if len(e.emb.Calls()) != 0 {
		t.Fatal("embedded a query for an unknown bank")
	}
	_, err = e.rc.Recall(context.Background(), Request{Bank: nope, Query: "  "})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("blank query: err = %v", err)
	}
}

func TestRecallFusionOrderGolden(t *testing.T) {
	sem := arm(armVector, "a", "b", "c")
	kw := arm(armText, "b", "c", "d")
	gr := arm(armGraph, "d")
	e := newEnv(t, envOpts{arms: []Arm{sem, kw, gr}})
	e.add(t, fact("a", "x a"), fact("b", "x b"), fact("c", "x c"), fact("d", "x d"))
	res := e.recall(t, Request{Query: "x", Overrides: alphas(0, 0, 0), Budget: domain.BudgetLow})
	if got := ids(res); !slices.Equal(got, []string{"b", "d", "c", "a"}) {
		t.Fatalf("order = %v, want [b d c a]", got)
	}
	if sem.seen.Limit != 100 {
		t.Fatalf("arm limit = %d, want 100 for BUDGET_LOW", sem.seen.Limit)
	}
	if s := res.Results[0].Scores; len(s.Arms) != 2 || s.Arms[armVector] != 0.99 || s.Arms[armText] != 1 {
		t.Fatalf("scores of b = %+v", s)
	}
}

func TestRecallCueFallbackCallsTimeParse(t *testing.T) {
	// Positive control for the no-cue test: a cue the rules can't resolve reaches TIME_PARSE once; the
	// unscripted call fails and recall degrades to no window instead of failing.
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "u1")}})
	e.add(t, fact("u1", "family dinner around the holidays"))
	res := e.recall(t, Request{Query: "what did we do around the holidays?", Trace: true})
	if n := len(e.llm.CallsFor(domain.ModelSlotTimeParse, "time_parse_system")); n != 1 {
		t.Fatalf("TIME_PARSE calls = %d, want 1 (all calls: %d)", n, len(e.llm.Calls()))
	}
	if res.Trace.Window != nil || !slices.Contains(ids(res), "u1") {
		t.Fatalf("window = %v results = %v", res.Trace.Window, ids(res))
	}
}

func TestRecallSeqSnapshot(t *testing.T) {
	sem := arm(armVector, "old", "late")
	e := newEnv(t, envOpts{arms: []Arm{sem}})
	e.add(t, fact("old", "early fact"))
	snapshot, err := e.store.Read().BankSeq(context.Background(), testBank)
	if err != nil {
		t.Fatal(err)
	}
	e.add(t, fact("late", "late fact")) // written after the snapshot; the stub arm still returns it
	res := e.recall(t, Request{Query: "fact", Seq: domain.SeqUpTo(0, snapshot)})
	if got := ids(res); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("snapshot results = %v, want [old]", got)
	}
	if got := sortedIDs(e.recall(t, Request{Query: "fact"})); !slices.Equal(got, []string{"late", "old"}) {
		t.Fatalf("unbounded results = %v", got)
	}
}

// failingArm is an Arm that always fails.
type failingArm struct{}

func (failingArm) Name() string { return armText }

func (failingArm) Description() string { return "fails" }

func (failingArm) Retrieve(context.Context, Query) ([]Hit, error) { return nil, domain.ErrUnavailable }

func TestRecallFailingArmFailsRecall(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "u1"), failingArm{}}})
	e.add(t, fact("u1", "doc"))
	_, err := e.rc.Recall(context.Background(), Request{Bank: testBank, Query: "doc"})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want the arm's UNAVAILABLE", err)
	}
}

func TestRecallLongQueryTruncated(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector)}})
	long := strings.TrimSpace(strings.Repeat("word ", 600))
	e.recall(t, Request{Query: long})
	calls := e.emb.Calls()
	if len(calls) != 1 || len(strings.Fields(calls[0].Texts[0])) != MaxQueryTokens {
		t.Fatalf("embedded query has %d words, want %d", len(strings.Fields(calls[0].Texts[0])), MaxQueryTokens)
	}
}
