package consolidate

import (
	"context"
	"errors"
	"maps"
	"memd/internal/domain"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"strings"
	"testing"
	"time"
)

// killAt is a progress hook that cancels the run at its n-th report (1-based).
type killAt struct {
	n, seen int
}

// Report counts reports and cancels at the n-th.
func (k *killAt) Report(context.Context, domain.Progress) error {
	k.seen++
	if k.seen == k.n {
		return domain.ErrCancelled
	}
	return nil
}

// TestRunReplayAfterKillAtThirdScope: a unit in 3 scopes (per_tag), killed after scope 2, then replayed:
// no duplicate observations, and it is stamped once (by the replay, in its last scope's transaction).
func TestRunReplayAfterKillAtThirdScope(t *testing.T) {
	e := newEnv(t, domain.BankConfig{
		Retain: &domain.RetainConfig{DefaultObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag}},
	})
	e.fact("f1", "Alice moved to Paris in May", "a", "b", "c")
	e.script(e.createEach("f1"))
	kill := &killAt{n: 3}
	e.hook = kill

	res, err := e.consolidator().Run(t.Context(), e.jobRun(autoJob()))
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("killed run: err = %v, want ErrCancelled", err)
	}
	if got := len(e.observations()); got != 2 {
		t.Fatalf("after the kill: %d observations, want 2 (scopes a and b)", got)
	}
	if u := e.unit("f1"); u.ConsolidatedAt != nil || res.Processed != 0 {
		t.Fatalf("unit stamped (%v) or processed (%d) before its last scope was decided", u.ConsolidatedAt,
			res.Processed)
	}

	e.hook = nil
	e.clock.Advance(time.Minute)
	replayAt := e.clock.Now()
	res = e.run(autoJob())
	if calls := len(e.batches()); calls != 3 {
		t.Fatalf("CONSOLIDATE calls = %d, want 3 (2 before the kill, only scope c on replay)", calls)
	}
	want := map[string]int{scopeOf("a").String(): 1, scopeOf("b").String(): 1, scopeOf("c").String(): 1}
	if got := perScope(t, e.observations(), "f1"); !maps.Equal(got, want) {
		t.Fatalf("observations per scope = %v, want %v", got, want)
	}
	u := e.unit("f1")
	if u.ConsolidatedAt == nil || !u.ConsolidatedAt.Equal(replayAt) {
		t.Fatalf("consolidated_at = %v, want the replay's time %v", u.ConsolidatedAt, replayAt)
	}
	if res.Processed != 1 || res.More {
		t.Fatalf("replay result = %+v, want Processed 1, More false", res)
	}
}

// perScope counts observations per scope key and checks each cites exactly source.
func perScope(t *testing.T, obs []domain.Unit, source domain.UnitID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, o := range obs {
		out[o.Scope().String()]++
		if !slices.Equal(o.SourceIDs, []domain.UnitID{uid(string(source))}) {
			t.Errorf("observation %s cites %v, want [%s]", o.ID, o.SourceIDs, source)
		}
	}
	return out
}

// wantBatches checks the fact ids of every CONSOLIDATE call so far.
func (e *env) wantBatches(want ...[]string) {
	e.t.Helper()
	if got := e.batches(); !slices.EqualFunc(got, want, slices.Equal) {
		e.t.Fatalf("batches = %v, want %v", got, want)
	}
}

// TestRunHalvesDownToPoisonUnit: a failing batch is halved until the poison unit fails alone; after the
// batch a health probe succeeds, the unit fails again when retried alone, and only then is it parked.
// Every other unit is stamped.
func TestRunHalvesDownToPoisonUnit(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{LLMBatchSize: ptr(4)}})
	e.fact("f1", "Alpha ships on Monday", "x")
	e.fact("f2", "Beta ships on Tuesday", "x")
	e.fact("f3", "Poison fact", "x")
	e.fact("f4", "Gamma ships on Friday", "x")
	e.script(fail(), e.createEach("f1", "f2"), fail(), fail(), e.createEach("f4"), ok(reply(nil, nil)), fail())
	res := e.run(autoJob())
	e.wantBatches([]string{"f1", "f2", "f3", "f4"}, []string{"f1", "f2"}, []string{"f3", "f4"}, []string{"f3"},
		[]string{"f4"}, []string{}, []string{"f3"})
	if u := e.unit("f3"); u.ConsolidationFailedAt == nil || u.ConsolidatedAt != nil {
		t.Fatalf("poison unit: failed_at %v consolidated_at %v, want parked only", u.ConsolidationFailedAt,
			u.ConsolidatedAt)
	}
	for _, id := range []string{"f1", "f2", "f4"} {
		if u := e.unit(id); u.ConsolidatedAt == nil || u.ConsolidationFailedAt != nil {
			t.Errorf("%s not stamped (consolidated_at %v, failed_at %v)", id, u.ConsolidatedAt, u.ConsolidationFailedAt)
		}
	}
	if got := len(e.observations()); got != 3 {
		t.Fatalf("%d observations, want 3", got)
	}
	if res.Processed != 4 || res.More {
		t.Fatalf("result = %+v, want Processed 4 (3 stamped + 1 parked), More false", res)
	}
}

// TestRunPoisonLastLeafParkedByProbe: a poison unit that fails alone as the last call of the run is parked
// once the health probe (a CONSOLIDATE call with no facts) succeeds.
func TestRunPoisonLastLeafParkedByProbe(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{LLMBatchSize: ptr(2)}})
	e.fact("f1", "Alpha ships", "x")
	e.fact("f2", "Poison fact", "x")
	e.script(fail(), e.createEach("f1"), fail(), ok(reply(nil, nil)), fail())
	res := e.run(autoJob())
	e.wantBatches([]string{"f1", "f2"}, []string{"f1"}, []string{"f2"}, []string{}, []string{"f2"})
	if e.unit("f1").ConsolidatedAt == nil || e.unit("f2").ConsolidationFailedAt == nil || res.Processed != 2 || res.More {
		t.Fatalf("result %+v; want f1 stamped, f2 parked, nothing more", res)
	}
}

// TestRunLonePoisonParked: a poison unit that is the bank's only unconsolidated unit is parked by the
// probe, so the job terminates instead of retrying forever.
func TestRunLonePoisonParked(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("p", "Poison fact", "x")
	e.script(fail(), ok(reply(nil, nil)), fail())
	if res := e.run(autoJob()); res.More || e.unit("p").ConsolidationFailedAt == nil {
		t.Fatalf("result %+v; want p parked", res)
	}
}

// TestRunPoisonAloneInScopeDoesNotBlockOthers: a poison unit alone in scope a is parked on the first run
// and scope b still consolidates.
func TestRunPoisonAloneInScopeDoesNotBlockOthers(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("p", "Poison fact", "a")
	e.fact("f1", "Bob rows", "b")
	e.script(fail(), ok(reply(nil, nil)), fail(), e.createEach("f1"))
	e.run(autoJob())
	e.wantBatches([]string{"p"}, []string{}, []string{"p"}, []string{"f1"})
	if e.unit("p").ConsolidationFailedAt == nil || e.unit("f1").ConsolidatedAt == nil {
		t.Fatal("want p parked and f1 stamped")
	}
}

// TestRunOutageDoesNotPark: when the gateway fails from the first call, nothing is parked; the run fails
// with UNAVAILABLE so the job retries.
func TestRunOutageDoesNotPark(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{LLMBatchSize: ptr(2)}})
	e.fact("f1", "One", "x")
	e.fact("f2", "Two", "x")
	e.script(fail())
	if _, err := e.consolidator().Run(t.Context(), e.jobRun(autoJob())); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	for _, id := range []string{"f1", "f2"} {
		if u := e.unit(id); u.ConsolidationFailedAt != nil || u.ConsolidatedAt != nil {
			t.Errorf("%s was stamped or parked during an outage", id)
		}
	}
	e.wantBatches([]string{"f1", "f2"}, []string{"f1"}, []string{"f2"}, []string{})
}

// TestRunOutageMidRunDoesNotPark: an outage that starts after earlier calls succeeded still parks
// nothing; the earlier scope's writes stay committed.
func TestRunOutageMidRunDoesNotPark(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Kim sails", "a")
	e.fact("f2", "Lee rows", "b")
	e.fact("f3", "Max surfs", "b")
	e.script(e.createEach("f1"), fail())
	if _, err := e.consolidator().Run(t.Context(), e.jobRun(autoJob())); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if e.unit("f1").ConsolidatedAt == nil || len(e.observations()) != 1 {
		t.Fatal("scope a's writes were not kept")
	}
	for _, id := range []string{"f2", "f3"} {
		if u := e.unit(id); u.ConsolidationFailedAt != nil || u.ConsolidatedAt != nil {
			t.Errorf("%s was stamped or parked during an outage", id)
		}
	}
}

// TestRunContinuationAtCap: a run stops at consolidation_max_units_per_run, enqueues its continuation
// (deduplicated) and reports More; the continuation finishes the rest.
func TestRunContinuationAtCap(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{MaxUnitsPerRun: ptr(2)}})
	for i, text := range []string{"One fact", "Two fact", "Three fact", "Four fact", "Five fact"} {
		e.fact("f"+string(rune('1'+i)), text, "x")
	}
	e.script(e.createEach("f1", "f2"), e.createEach("f3", "f4"), e.createEach("f5"))
	if res := e.run(autoJob()); res.Processed != 2 || !res.More {
		t.Fatalf("first run = %+v, want Processed 2, More true", res)
	}
	if e.unit("f3").ConsolidatedAt != nil {
		t.Fatal("f3 consolidated beyond the cap")
	}
	e.wantPending(domain.OperationKindConsolidate, 1)
	// A second run also stops at the cap; its continuation joins the queued one instead of piling up.
	if res := e.run(autoJob()); !res.More || res.Processed != 2 {
		t.Fatalf("second run = %+v; want Processed 2, More true", res)
	}
	e.wantPending(domain.OperationKindConsolidate, 1)
	jobs, err := e.store.Claim(t.Context(), "w1", []domain.OperationKind{domain.OperationKindConsolidate}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim continuation: %d jobs, %v", len(jobs), err)
	}
	if jobs[0].SerialKey != "consolidate" || string(jobs[0].Payload) != "{}" {
		t.Fatalf("continuation = %+v, want serial key consolidate and payload {}", jobs[0])
	}
	if res := e.run(jobs[0]); res.More || res.Processed != 1 || e.unit("f5").ConsolidatedAt == nil {
		t.Fatalf("continuation = %+v; want f5 done and nothing more", res)
	}
	e.wantPending(domain.OperationKindConsolidate, 0)
}

// TestRunStrategyOverridesBankNotRequest: the matching strategy's mission and CONSOLIDATE binding replace
// the bank's for its scopes only, and a request override (manual Consolidate) beats the strategy.
func TestRunStrategyOverridesBankNotRequest(t *testing.T) {
	cfg := domain.BankConfig{Observations: &domain.ObservationConfig{
		Mission: "BANK-MISSION", Strategies: []domain.ConsolidationStrategy{{
			Name:             "team",
			Scopes:           []domain.ScopePattern{{Tags: []string{"team"}}},
			Mission:          "STRATEGY-MISSION",
			ConsolidateModel: consolidateModel("strategy-model"),
		}},
	}, Models: &domain.ModelBindings{Consolidate: consolidateModel("bank-model")}}
	e := newEnv(t, cfg)
	e.fact("f1", "The team meets on Monday", "team")
	e.fact("f2", "The other group meets on Friday", "other")
	e.script(ok(reply(nil, nil)))
	e.run(autoJob())
	got := map[string]string{}
	for _, c := range e.calls(domain.ModelSlotConsolidate) {
		got[pgtest.Name(schemaFactIDs(c.Schema)[0])] = c.Binding.Model + "|" + missionOf(c.User)
	}
	if got["f1"] != "strategy-model|STRATEGY-MISSION" || got["f2"] != "bank-model|BANK-MISSION" {
		t.Fatalf("automatic run: %v; want the strategy for team, the bank for other", got)
	}

	e.fact("f3", "The team ships on Friday", "team")
	j := newJob(nil, &domain.BankConfig{
		Observations: &domain.ObservationConfig{Mission: "REQUEST-MISSION"},
		Models:       &domain.ModelBindings{Consolidate: consolidateModel("request-model")},
	})
	j.ID = pgtest.ID[domain.OperationID]("op-manual")
	e.run(j)
	calls := e.calls(domain.ModelSlotConsolidate)
	last := calls[len(calls)-1]
	if last.Binding.Model != "request-model" || missionOf(last.User) != "REQUEST-MISSION" {
		t.Fatalf("manual run in the team scope: model %q mission %q; want the request's", last.Binding.Model,
			missionOf(last.User))
	}
}

// missionOf extracts the mission line of a CONSOLIDATE user prompt.
func missionOf(user string) string {
	_, rest, ok := strings.Cut(user, "## MISSION\n\n")
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(rest, "\n")
	return line
}

// TestRunDedupMerge: a create near an existing observation (cosine >= dedup_threshold) that DEDUP_CHECK
// calls a duplicate is folded into the twin: merged text, new version, the new fact as evidence, and no
// new observation.
func TestRunDedupMerge(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.5)}})
	e.fact("f0", "Alice lives in Paris", "x")
	e.observation("obs-twin", "Alice lives in Paris", []string{"x"}, "f0")
	e.fact("f1", "Alice lives in Paris France", "x")
	e.script(ok(reply([]create{{Text: "Alice lives in Paris France", SourceFactIDs: []string{"f1"}}}, nil)))
	e.dedupReply(`{"action":"merge","text":"Alice lives in Paris, France","reason":"same fact"}`)
	res := e.run(autoJob())
	obs := e.observations()
	if len(obs) != 1 {
		t.Fatalf("%d observations, want only the twin", len(obs))
	}
	twin := obs[0]
	if pgtest.Name(twin.ID) != "obs-twin" || twin.Text != "Alice lives in Paris, France" || twin.Version != 2 ||
		!slices.Equal(sortedNames(twin.SourceIDs), []string{"f0", "f1"}) || twin.ProofCount != 2 {
		t.Fatalf("twin = %s %q v%d sources %v proof %d; want merged text, v2, [f0 f1], 2",
			twin.ID, twin.Text, twin.Version, twin.SourceIDs, twin.ProofCount)
	}
	if len(e.calls(domain.ModelSlotDedupCheck)) != 1 {
		t.Fatal("want exactly one DEDUP_CHECK call")
	}
	if e.unit("f1").ConsolidatedAt == nil || len(res.Changed) != 1 {
		t.Fatalf("f1 not stamped or changed scopes = %v", res.Changed)
	}
	if !slices.ContainsFunc(res.Usage, func(u domain.Usage) bool { return u.Slot == domain.ModelSlotDedupCheck }) {
		t.Fatalf("usage %v lacks DEDUP_CHECK", res.Usage)
	}
}

// TestRunDedupKeep: DEDUP_CHECK keep leaves the twin alone and creates the new observation.
func TestRunDedupKeep(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.5)}})
	e.fact("f0", "Alice owns 3 cats", "x")
	e.observation("obs-twin", "Alice owns 3 cats", []string{"x"}, "f0")
	e.fact("f1", "Alice owns 4 cats", "x")
	e.script(ok(reply([]create{{Text: "Alice owns 4 cats", SourceFactIDs: []string{"f1"}}}, nil)))
	e.dedupReply(`{"action":"keep","text":"","reason":"different numbers"}`)
	e.run(autoJob())
	obs := e.observations()
	if len(obs) != 2 {
		t.Fatalf("%d observations, want the twin and the new one", len(obs))
	}
	for _, o := range obs {
		switch pgtest.Name(o.ID) {
		case "obs-twin":
			if o.Text != "Alice owns 3 cats" || o.Version != 1 {
				t.Errorf("twin changed: %q v%d", o.Text, o.Version)
			}
		default:
			if o.Text != "Alice owns 4 cats" || !slices.Equal(names(o.SourceIDs), []string{"f1"}) || o.Version != 1 {
				t.Errorf("new observation = %q v%d sources %v", o.Text, o.Version, o.SourceIDs)
			}
		}
	}
	if len(e.calls(domain.ModelSlotDedupCheck)) != 1 {
		t.Fatal("want exactly one DEDUP_CHECK call")
	}
}

// TestRunDedupBelowThresholdSkipsCheck: no observation at or above the threshold means no DEDUP_CHECK.
func TestRunDedupBelowThresholdSkipsCheck(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Alice owns cats", "x")
	e.observation("obs-1", "Alice owns cats", []string{"x"}, "f0")
	e.fact("f1", "Bob drives trucks", "x")
	e.script(e.createEach("f1"))
	e.run(autoJob())
	if n := len(e.calls(domain.ModelSlotDedupCheck)); n != 0 {
		t.Fatalf("DEDUP_CHECK calls = %d, want 0", n)
	}
	if len(e.observations()) != 2 {
		t.Fatal("want two observations")
	}
}
