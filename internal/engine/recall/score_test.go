package recall

import (
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// alphas returns overrides setting the three boost alphas.
func alphas(recency, temporal, proof float64) *domain.BankConfig {
	return recallCfg(domain.RecallConfig{RecencyAlpha: &recency, TemporalAlpha: &temporal, ProofAlpha: &proof})
}

// flatReranker makes every document score 0.5, so only the boosts separate candidates.
func flatReranker(e *env) { e.rr.SetFunc(func(string, string) float32 { return 0.5 }) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBoostRecencyIsolated(t *testing.T) {
	sem := arm(ArmSemantic, "old", "new")
	e := newEnv(t, envOpts{rerank: true, arms: []Retriever{sem}})
	flatReranker(e)
	old, fresh := fact("old", "the old fact"), fact("new", "the new fact")
	old.OccurredStart, fresh.OccurredStart = at(-730), at(-1)
	e.add(t, old, fresh)

	res := e.recall(t, RecallRequest{Query: "fact", Overrides: alphas(0.2, 0, 0)})
	if got := ids(res); !slices.Equal(got, []string{"new", "old"}) {
		t.Fatalf("order = %v, want [new old]", got)
	}
	wantNew := 0.5 * (1 + 0.2*((1-1.0/365)-0.5))
	wantOld := 0.5 * (1 + 0.2*(0.1-0.5)) // two years old: the 0.1 floor
	if !near(res.Results[0].Scores.Final, wantNew) || !near(res.Results[1].Scores.Final, wantOld) {
		t.Fatalf("finals = %v, %v; want %v, %v", res.Results[0].Scores.Final, res.Results[1].Scores.Final,
			wantNew, wantOld)
	}
	// With the alpha at 0 the recency signal is gone and fusion order (old first) stands.
	res = e.recall(t, RecallRequest{Query: "fact", Overrides: alphas(0, 0, 0)})
	if got := ids(res); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("order without recency = %v", got)
	}
}

func TestBoostTemporalIsolated(t *testing.T) {
	sem := arm(ArmSemantic, "a", "b")
	tmp := &stubArm{name: ArmTemporal, hits: []port.Hit{{ID: "b", Score: 1}, {ID: "a", Score: 0.2}}}
	e := newEnv(t, envOpts{rerank: true, arms: []Retriever{sem, tmp}})
	flatReranker(e)
	a, b := fact("a", "alpha event"), fact("b", "beta event")
	a.OccurredStart, b.OccurredStart = at(-1), at(-400) // recency would favour a; its alpha is 0
	e.add(t, a, b)
	w := &domain.TimeWindow{Start: testNow.AddDate(-2, 0, 0), End: testNow}

	res := e.recall(t, RecallRequest{Query: "event", Window: w, Overrides: alphas(0, 0.2, 0)})
	if got := ids(res); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("order = %v, want [b a]", got)
	}
	if f := res.Results[0].Scores.Final; !near(f, 0.5*1.1) {
		t.Fatalf("final(b) = %v, want 0.55", f)
	}
	if f := res.Results[1].Scores.Final; !near(f, 0.5*(1+0.2*(0.2-0.5))) {
		t.Fatalf("final(a) = %v, want 0.47", f)
	}
	if tmp.called != 1 {
		t.Fatalf("temporal arm called %d times", tmp.called)
	}
}

func TestBoostProofIsolated(t *testing.T) {
	sem := arm(ArmSemantic, "o1", "o50")
	e := newEnv(t, envOpts{rerank: true, arms: []Retriever{sem}})
	flatReranker(e)
	o1 := domain.Unit{ID: "o1", Bank: testBank, Type: domain.FactTypeObservation, Text: "weak obs", ProofCount: 1}
	o50 := domain.Unit{ID: "o50", Bank: testBank, Type: domain.FactTypeObservation, Text: "strong obs", ProofCount: 50}
	e.add(t, o1, o50)

	res := e.recall(t, RecallRequest{Query: "obs", Overrides: alphas(0, 0, 0.1)})
	if got := ids(res); !slices.Equal(got, []string{"o50", "o1"}) {
		t.Fatalf("order = %v, want [o50 o1]", got)
	}
	want := 0.5 * (1 + 0.1*(0.5+math.Log(50)/10-0.5))
	if !near(res.Results[0].Scores.Final, want) || !near(res.Results[1].Scores.Final, 0.5) {
		t.Fatalf("finals = %v, %v; want %v, 0.5", res.Results[0].Scores.Final, res.Results[1].Scores.Final, want)
	}
}

func TestBoostNeutral(t *testing.T) {
	s := settingsOf(domain.BankConfig{})
	q := &ParsedQuery{At: testNow}
	timeless := domain.Unit{Type: domain.FactTypeWorld, Timeless: true, MentionedAt: at(-1000)}
	undated := domain.Unit{Type: domain.FactTypeObservation}
	for _, u := range []domain.Unit{timeless, undated} {
		b := boost(Candidate{Unit: &u}, q, s)
		if b != (Boosts{Recency: 1, Temporal: 1, Proof: 1}) {
			t.Fatalf("boost(%+v) = %+v, want neutral", u, b)
		}
	}
	// A unit dated to a whole calendar month still in progress is capped at neutral, not boosted.
	start, end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	month := domain.Unit{OccurredStart: &start, OccurredEnd: &end}
	if r := recencySignal(month, testNow, 365); r != 0.5 {
		t.Fatalf("coarse month recency = %v, want 0.5", r)
	}
	// mentioned_at is the fallback instant.
	mentioned := domain.Unit{MentionedAt: at(-73)}
	if r := recencySignal(mentioned, testNow, 365); !near(r, 0.8) {
		t.Fatalf("mentioned recency = %v, want 0.8", r)
	}
}
