package consolidate

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
	"time"
)

// killAt is a Checkpointer that cancels the run at its n-th report (1-based).
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
	e := newEnv(t, domain.BankConfig{DefaultObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag}})
	e.fact("f1", "Alice moved to Paris in May", "a", "b", "c")
	kill := &killAt{n: 3}
	e.deps.Checkpoint = func(port.Job) Checkpointer { return kill }

	res, err := e.consolidator().Run(t.Context(), autoJob())
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("killed run: err = %v, want ErrCancelled", err)
	}
	if got := len(e.observations()); got != 2 {
		t.Fatalf("after the kill: %d observations, want 2 (scopes a and b)", got)
	}
	if u := e.unit("f1"); u.ConsolidatedAt != nil {
		t.Fatalf("unit stamped before its last scope was decided")
	}
	if res.Processed != 0 {
		t.Fatalf("killed run processed %d, want 0", res.Processed)
	}

	e.deps.Checkpoint = nil
	e.clock.Advance(time.Minute)
	replayAt := e.clock.Now()
	res, err = e.consolidator().Run(t.Context(), autoJob())
	if err != nil {
		t.Fatal(err)
	}
	if calls := len(e.llm.callsFor(domain.ModelSlotConsolidate)); calls != 3 {
		t.Fatalf("CONSOLIDATE calls = %d, want 3 (2 before the kill, only scope c on replay)", calls)
	}
	perScope := map[string]int{}
	for _, o := range e.observations() {
		perScope[o.Scope().String()]++
		if !slices.Equal(o.SourceIDs, []port.UnitID{"f1"}) {
			t.Errorf("observation %s cites %v, want [f1]", o.ID, o.SourceIDs)
		}
	}
	want := map[string]int{scopeOf("a").String(): 1, scopeOf("b").String(): 1, scopeOf("c").String(): 1}
	if len(perScope) != 3 || perScope[scopeOf("a").String()] != 1 || perScope[scopeOf("b").String()] != 1 ||
		perScope[scopeOf("c").String()] != 1 {
		t.Fatalf("observations per scope = %v, want %v", perScope, want)
	}
	u := e.unit("f1")
	if u.ConsolidatedAt == nil || !u.ConsolidatedAt.Equal(replayAt) {
		t.Fatalf("consolidated_at = %v, want the replay's time %v", u.ConsolidatedAt, replayAt)
	}
	if res.Processed != 1 || res.More {
		t.Fatalf("replay result = %+v, want Processed 1, More false", res)
	}
}

// TestRunHalvesDownToPoisonUnit: a failing batch is halved until the poison unit fails alone and is
// parked; every other unit is stamped.
func TestRunHalvesDownToPoisonUnit(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationLLMBatchSize: ptr(4)})
	e.fact("f1", "Alpha ships on Monday", "x")
	e.fact("f2", "Beta ships on Tuesday", "x")
	e.fact("f3", "Poison fact", "x")
	e.fact("f4", "Gamma ships on Friday", "x")
	var sizes []int
	e.llm.consolidate = func(req port.StructuredRequest, ids []string) (string, error) {
		sizes = append(sizes, len(ids))
		if hasFact(ids, "f3") {
			return "", errPoison
		}
		return e.createEach(req, ids)
	}
	res, err := e.consolidator().Run(t.Context(), autoJob())
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{4, 2, 2, 1, 1}; !slices.Equal(sizes, want) {
		t.Fatalf("batch sizes = %v, want %v", sizes, want)
	}
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

// TestRunOutageDoesNotPark: when no call of the run succeeds, nothing is parked; the run fails with
// UNAVAILABLE so the job retries.
func TestRunOutageDoesNotPark(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationLLMBatchSize: ptr(2)})
	e.fact("f1", "One", "x")
	e.fact("f2", "Two", "x")
	e.llm.consolidate = func(port.StructuredRequest, []string) (string, error) { return "", errors.New("gateway down") }
	_, err := e.consolidator().Run(t.Context(), autoJob())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	for _, id := range []string{"f1", "f2"} {
		if u := e.unit(id); u.ConsolidationFailedAt != nil || u.ConsolidatedAt != nil {
			t.Errorf("%s was stamped or parked during an outage", id)
		}
	}
	if calls := len(e.llm.callsFor(domain.ModelSlotConsolidate)); calls != 3 {
		t.Fatalf("calls = %d, want 3 (the batch, then each half)", calls)
	}
}

// TestRunContinuationAtCap: a run stops at consolidation_max_units_per_run, enqueues its continuation
// (deduplicated) and reports More; the continuation finishes the rest.
func TestRunContinuationAtCap(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationMaxUnitsPerRun: ptr(2)})
	e.fact("f1", "One fact", "x")
	e.fact("f2", "Two fact", "x")
	e.fact("f3", "Three fact", "x")
	e.fact("f4", "Four fact", "x")
	e.fact("f5", "Five fact", "x")
	res, err := e.consolidator().Run(t.Context(), autoJob())
	if err != nil {
		t.Fatal(err)
	}
	if res.Processed != 2 || !res.More {
		t.Fatalf("first run = %+v, want Processed 2, More true", res)
	}
	if e.unit("f3").ConsolidatedAt != nil {
		t.Fatal("f3 consolidated beyond the cap")
	}
	pending := e.jobs(domain.OperationKindConsolidate)
	if len(pending) != 1 {
		t.Fatalf("pending consolidate jobs = %d, want 1 continuation", len(pending))
	}
	// A second run also stops at the cap; its continuation joins the queued one instead of piling up.
	if res, err := e.consolidator().Run(t.Context(), autoJob()); err != nil || !res.More || res.Processed != 2 {
		t.Fatalf("second run = %+v, %v; want Processed 2, More true", res, err)
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 1 {
		t.Fatalf("pending consolidate jobs = %d, want the one continuation", n)
	}
	jobs, err := e.store.Claim(t.Context(), "w1", []port.JobKind{domain.OperationKindConsolidate}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim continuation: %d jobs, %v", len(jobs), err)
	}
	if jobs[0].SerialKey != SerialKey || string(jobs[0].Payload) != "{}" {
		t.Fatalf("continuation = %+v, want serial key %q and payload {}", jobs[0], SerialKey)
	}
	res, err = e.consolidator().Run(t.Context(), jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if res.More || res.Processed != 1 || e.unit("f5").ConsolidatedAt == nil {
		t.Fatalf("continuation = %+v, f5 stamped %v; want everything done", res, e.unit("f5").ConsolidatedAt)
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 0 {
		t.Fatalf("pending consolidate jobs after the last run = %d, want 0", n)
	}
}

// TestRunStrategyOverridesBankNotRequest: the matching strategy's mission and CONSOLIDATE binding replace
// the bank's for its scopes only, and a request override (manual Consolidate) beats the strategy.
func TestRunStrategyOverridesBankNotRequest(t *testing.T) {
	cfg := domain.BankConfig{
		ObservationsMission: "BANK-MISSION",
		Models:              []domain.SlotBinding{{Slot: domain.ModelSlotConsolidate, Binding: domain.ModelBinding{Model: "bank-model"}}},
		ConsolidationStrategies: []domain.ConsolidationStrategy{{
			Name:                "team",
			Scopes:              []domain.ScopePattern{{Tags: []string{"team"}}},
			ObservationsMission: "STRATEGY-MISSION",
			Models: []domain.SlotBinding{{
				Slot: domain.ModelSlotConsolidate, Binding: domain.ModelBinding{Model: "strategy-model"},
			}},
		}},
	}
	e := newEnv(t, cfg)
	e.fact("f1", "The team meets on Monday", "team")
	e.fact("f2", "The other group meets on Friday", "other")
	if _, err := e.consolidator().Run(t.Context(), autoJob()); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range e.llm.callsFor(domain.ModelSlotConsolidate) {
		got[schemaFactIDs(c.Schema)[0]] = c.Binding.Model + "|" + missionOf(c.User)
	}
	if got["f1"] != "strategy-model|STRATEGY-MISSION" || got["f2"] != "bank-model|BANK-MISSION" {
		t.Fatalf("automatic run: %v; want the strategy for team, the bank for other", got)
	}

	e.fact("f3", "The team ships on Friday", "team")
	j := NewJob(bank, nil, &domain.BankConfig{
		ObservationsMission: "REQUEST-MISSION",
		Models:              []domain.SlotBinding{{Slot: domain.ModelSlotConsolidate, Binding: domain.ModelBinding{Model: "request-model"}}},
	})
	j.ID = "op-manual"
	if _, err := e.consolidator().Run(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	calls := e.llm.callsFor(domain.ModelSlotConsolidate)
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
	e := newEnv(t, domain.BankConfig{DedupThreshold: ptr(0.5)})
	e.fact("f0", "Alice lives in Paris", "x")
	e.observation("obs-twin", "Alice lives in Paris", []string{"x"}, "f0")
	e.fact("f1", "Alice lives in Paris France", "x")
	e.llm.consolidate = func(port.StructuredRequest, []string) (string, error) {
		return reply([]create{{Text: "Alice lives in Paris France", SourceFactIDs: []string{"f1"}}}, nil), nil
	}
	e.llm.dedup = func(port.StructuredRequest) (string, error) {
		return `{"action":"merge","text":"Alice lives in Paris, France","reason":"same fact"}`, nil
	}
	res, err := e.consolidator().Run(t.Context(), autoJob())
	if err != nil {
		t.Fatal(err)
	}
	obs := e.observations()
	if len(obs) != 1 {
		t.Fatalf("%d observations, want only the twin", len(obs))
	}
	twin := obs[0]
	if twin.ID != "obs-twin" || twin.Text != "Alice lives in Paris, France" || twin.Version != 2 ||
		!slices.Equal(twin.SourceIDs, []port.UnitID{"f0", "f1"}) || twin.ProofCount != 2 {
		t.Fatalf("twin = %s %q v%d sources %v proof %d; want merged text, v2, [f0 f1], 2",
			twin.ID, twin.Text, twin.Version, twin.SourceIDs, twin.ProofCount)
	}
	if len(e.llm.callsFor(domain.ModelSlotDedupCheck)) != 1 {
		t.Fatal("want exactly one DEDUP_CHECK call")
	}
	if e.unit("f1").ConsolidatedAt == nil || len(res.Changed) != 1 {
		t.Fatalf("f1 not stamped or changed scopes = %v", res.Changed)
	}
	if !slices.ContainsFunc(res.Usage, func(u port.Usage) bool { return u.Slot == domain.ModelSlotDedupCheck }) {
		t.Fatalf("usage %v lacks DEDUP_CHECK", res.Usage)
	}
}

// TestRunDedupKeep: DEDUP_CHECK keep leaves the twin alone and creates the new observation.
func TestRunDedupKeep(t *testing.T) {
	e := newEnv(t, domain.BankConfig{DedupThreshold: ptr(0.5)})
	e.fact("f0", "Alice owns 3 cats", "x")
	e.observation("obs-twin", "Alice owns 3 cats", []string{"x"}, "f0")
	e.fact("f1", "Alice owns 4 cats", "x")
	e.llm.consolidate = func(port.StructuredRequest, []string) (string, error) {
		return reply([]create{{Text: "Alice owns 4 cats", SourceFactIDs: []string{"f1"}}}, nil), nil
	}
	e.llm.dedup = func(port.StructuredRequest) (string, error) {
		return `{"action":"keep","text":"","reason":"different numbers"}`, nil
	}
	if _, err := e.consolidator().Run(t.Context(), autoJob()); err != nil {
		t.Fatal(err)
	}
	obs := e.observations()
	if len(obs) != 2 {
		t.Fatalf("%d observations, want the twin and the new one", len(obs))
	}
	for _, o := range obs {
		switch o.ID {
		case "obs-twin":
			if o.Text != "Alice owns 3 cats" || o.Version != 1 {
				t.Errorf("twin changed: %q v%d", o.Text, o.Version)
			}
		default:
			if o.Text != "Alice owns 4 cats" || !slices.Equal(o.SourceIDs, []port.UnitID{"f1"}) || o.Version != 1 {
				t.Errorf("new observation = %q v%d sources %v", o.Text, o.Version, o.SourceIDs)
			}
		}
	}
	if len(e.llm.callsFor(domain.ModelSlotDedupCheck)) != 1 {
		t.Fatal("want exactly one DEDUP_CHECK call")
	}
}

// TestRunDedupBelowThresholdSkipsCheck: no observation at or above the threshold means no DEDUP_CHECK.
func TestRunDedupBelowThresholdSkipsCheck(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Alice owns cats", "x")
	e.observation("obs-1", "Alice owns cats", []string{"x"}, "f0")
	e.fact("f1", "Bob drives trucks", "x")
	if _, err := e.consolidator().Run(t.Context(), autoJob()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.llm.callsFor(domain.ModelSlotDedupCheck)); n != 0 {
		t.Fatalf("DEDUP_CHECK calls = %d, want 0", n)
	}
	if len(e.observations()) != 2 {
		t.Fatal("want two observations")
	}
}
