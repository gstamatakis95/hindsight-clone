package recall

import (
	"math"
	"memd/internal/domain"
	"memd/internal/store/postgres/pgtest"
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
	sem := arm(armVector, "old", "new")
	e := newEnv(t, envOpts{rerank: true, arms: []Arm{sem}})
	flatReranker(e)
	old, fresh := fact("old", "the old fact"), fact("new", "the new fact")
	old.OccurredStart, fresh.OccurredStart = at(-730), at(-1)
	e.add(t, old, fresh)

	res := e.recall(t, Request{Query: "fact", Overrides: alphas(0.2, 0, 0)})
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
	res = e.recall(t, Request{Query: "fact", Overrides: alphas(0, 0, 0)})
	if got := ids(res); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("order without recency = %v", got)
	}
}

func TestBoostTemporalIsolated(t *testing.T) {
	// A real one-year window: 2025-06-01T12:00 .. 2026-06-01T12:00, midpoint 2025-12-01T00:00, half-width
	// 182.5 days. Hindsight's linear proximity: 1 - min(|best - mid| / 182.5, 1).
	w := &domain.TimeWindow{Start: testNow.AddDate(-1, 0, 0), End: testNow}
	mid := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	sem := arm(armVector, "far", "centre", "span", "undated", "outside")
	// The adapter's own scores order the arm (buckets) but must not reach the boost.
	tmp := &stubArm{name: armTemporal, window: true, hits: []Hit{
		{ID: uid("far"), Score: 1},
		{ID: uid("centre"), Score: 0.01},
		{ID: uid("span"), Score: 0.5},
		{ID: uid("undated"), Score: 0.9},
	}}
	e := newEnv(t, envOpts{rerank: true, arms: []Arm{sem, tmp}})
	flatReranker(e)
	far, centre, span := fact("far", "far event"), fact("centre", "centre event"), fact("span", "span event")
	undated, outside := fact("undated", "undated event"), fact("outside", "outside event")
	farAt, spanStart, spanEnd := mid.AddDate(0, 0, -91), mid.AddDate(0, 0, -10), mid.AddDate(0, 0, 30)
	far.OccurredStart = &farAt
	centre.MentionedAt = &mid                                   // no occurrence: mentioned_at places it
	span.OccurredStart, span.OccurredEnd = &spanStart, &spanEnd // midpoint = mid + 10 days
	outside.OccurredStart = &mid                                // not returned by the temporal arm: neutral
	e.add(t, far, centre, span, undated, outside)

	res := e.recall(t, Request{Query: "event", Window: w, Overrides: alphas(0, 0.2, 0)})
	want := map[string]float64{
		"centre":  1,
		"span":    1 - 10/182.5,
		"far":     1 - 91/182.5,
		"undated": 0.5,
		"outside": 0.5,
	}
	for _, m := range res.Results {
		prox := want[pgtest.Name(m.Unit.ID)]
		if f := m.Scores.Final; !near(f, 0.5*(1+0.2*(prox-0.5))) {
			t.Errorf("final(%s) = %v, want proximity %v", m.Unit.ID, f, prox)
		}
	}
	if got := ids(res); !slices.Equal(got[:3], []string{"centre", "span", "far"}) || len(got) != 5 {
		t.Fatalf("order = %v, want centre, span, far first", got)
	}
	if tmp.called != 1 {
		t.Fatalf("temporal arm called %d times", tmp.called)
	}
}

func TestTemporalProximityWindows(t *testing.T) {
	at := testNow
	u := domain.Unit{OccurredStart: &at}
	if p := temporalProximity(u, domain.TimeWindow{Start: testNow, End: testNow}, 365); p != 1 {
		t.Fatalf("zero-length window = %v, want 1", p)
	}
	hour := domain.TimeWindow{Start: testNow, End: testNow.Add(time.Hour)}
	if p := temporalProximity(domain.Unit{}, hour, 365); p != 0.5 {
		t.Fatalf("undated = %v, want 0.5", p)
	}
	// One open bound: anchored on the set bound, scaled by recency_horizon_days.
	open := domain.TimeWindow{Start: testNow.AddDate(0, 0, -73)}
	if p := temporalProximity(u, open, 365); !near(p, 0.8) {
		t.Fatalf("open end = %v, want 0.8", p)
	}
	if p := temporalProximity(u, domain.TimeWindow{End: testNow.AddDate(0, 0, 400)}, 365); p != 0 {
		t.Fatalf("beyond the horizon = %v, want 0", p)
	}
}

func TestBoostProofIsolated(t *testing.T) {
	sem := arm(armVector, "o1", "o50")
	e := newEnv(t, envOpts{rerank: true, arms: []Arm{sem}})
	flatReranker(e)
	o1 := domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "weak obs", ProofCount: 1}
	o50 := domain.Unit{
		ID: uid("o50"), Bank: testBank, Type: domain.FactTypeObservation, Text: "strong obs", ProofCount: 50,
	}
	e.add(t, o1, o50)

	res := e.recall(t, Request{Query: "obs", Overrides: alphas(0, 0, 0.1)})
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
	q := &parsedQuery{At: testNow}
	timeless := domain.Unit{Type: domain.FactTypeWorld, Timeless: true, MentionedAt: at(-1000)}
	undated := domain.Unit{Type: domain.FactTypeObservation}
	for _, u := range []domain.Unit{timeless, undated} {
		b := boost(candidate{Unit: &u}, q, s, nil)
		if b != (boosts{Recency: 1, Temporal: 1, Proof: 1}) {
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
