package mentalmodel

import (
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
	"time"
)

// TestUntaggedWriteNeverStalesTaggedModel checks the staleness rule, including a tagged model that reads
// untagged memories (tags_match any), and that a retracted source stales the model.
func TestUntaggedWriteNeverStalesTaggedModel(t *testing.T) {
	e := newEnv(t)
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Scope.Tags.Match = domain.TagsMatchAny
	m := e.addModel(spec)
	cases := []struct {
		name  string
		write func() domain.Unit
		stale bool
	}{
		{"untagged", func() domain.Unit { return e.addUnit(domain.FactTypeWorld, "untagged fact") }, false},
		{"other tag", func() domain.Unit { return e.addUnit(domain.FactTypeWorld, "x", "other") }, false},
		{"untagged observation", func() domain.Unit { return e.addUnit(domain.FactTypeObservation, "obs") }, false},
		{"in scope", func() domain.Unit { return e.addUnit(domain.FactTypeObservation, "y", "proj") }, true},
	}
	for _, c := range cases {
		u := c.write()
		stale, upTo, err := e.svc.needsRefresh(e.ctx, m, domain.BankConfig{})
		if err != nil || stale != c.stale || upTo != u.Seq {
			t.Fatalf("%s: stale %v (want %v) upTo %d err %v", c.name, stale, c.stale, upTo, err)
		}
		m.LastSeqSeen = u.Seq
	}
	src := e.addUnit(domain.FactTypeWorld, "cited", "proj")
	m.LastSeqSeen, m.SourceIDs = src.Seq, []domain.UnitID{src.ID}
	if stale, _, _ := e.svc.needsRefresh(e.ctx, m, domain.BankConfig{}); stale {
		t.Fatal("fresh model reported stale")
	}
	e.archive(src.ID)
	if stale, _, _ := e.svc.needsRefresh(e.ctx, m, domain.BankConfig{}); !stale {
		t.Fatal("retracted source did not stale the model")
	}
}

// TestDecide checks pause, freshness, debounce, the interval floor and run-now.
func TestDecide(t *testing.T) {
	e := newEnv(t)
	m := e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	now := e.clock.Now()
	if d, _ := e.svc.decide(e.ctx, m, now); d.Kind != decisionSkip || d.Reason != reasonNotStale {
		t.Fatalf("fresh: %+v", d)
	}
	e.addUnit(domain.FactTypeWorld, "new", "proj")
	if d, _ := e.svc.decide(e.ctx, m, now); d.Kind != decisionPark || !d.Until.Equal(now.Add(30*time.Second)) {
		t.Fatalf("debounce: %+v", d)
	}
	floor, last := 600, now.Add(-time.Minute)
	m.Spec.Schedule.MinIntervalSeconds, m.LastRefreshedAt = &floor, &last
	if d, _ := e.svc.decide(e.ctx, m, now); d.Kind != decisionPark || d.Reason != domain.RefreshReasonIntervalFloor ||
		!d.Until.Equal(last.Add(10*time.Minute)) {
		t.Fatalf("floor: %+v", d)
	}
	zero := 0
	m.Spec.Schedule.MinIntervalSeconds = nil
	m.Spec.Overrides = &domain.BankConfig{MentalModels: &domain.MentalModelConfig{RefreshDebounceSeconds: &zero}}
	if d, _ := e.svc.decide(e.ctx, m, now); d.Kind != decisionRunNow {
		t.Fatalf("run now: %+v", d)
	}
	m.AutoRefreshPaused = true
	if d, _ := e.svc.decide(e.ctx, m, now); d.Kind != decisionSkip || d.Reason != reasonPaused {
		t.Fatalf("paused: %+v", d)
	}
}

// autoTrigger runs the automatic trigger path for model id (decide, then a parked or immediate enqueue) and
// returns how many refreshes it inserted.
func autoTrigger(e *env, id domain.MentalModelID) (int, error) {
	return e.svc.triggerAll(e.ctx, []domain.MentalModel{e.model(id)})
}

// claimRefresh claims the due refresh job, if any.
func claimRefresh(e *env) (store.Job, jobs.RefreshPayload, bool) {
	e.t.Helper()
	claimed := e.claimDue(domain.OperationKindRefreshMentalModel)
	if len(claimed) == 0 {
		return store.Job{}, jobs.RefreshPayload{}, false
	}
	var p jobs.RefreshPayload
	if err := json.Unmarshal(claimed[0].Payload, &p); err != nil {
		e.t.Fatal(err)
	}
	return claimed[0], p, true
}

// TestJoiningQueuedRefreshWithForceFullUpgrades checks the queued-refresh semantics: automatic triggers
// park and join one refresh; a manual force_full request joins it (joined_queued), ORs force_full, sets
// its overrides and strategy, and releases the parked job.
func TestJoiningQueuedRefreshWithForceFullUpgrades(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Schedule.AfterConsolidation = true
	e.addModel(spec)
	e.addUnit(domain.FactTypeWorld, "new", "proj")
	if n, err := autoTrigger(e, "status"); err != nil || n != 1 {
		t.Fatalf("first trigger inserted %d: %v", n, err)
	}
	if n, err := autoTrigger(e, "status"); err != nil || n != 0 {
		t.Fatalf("second trigger must join, inserted %d: %v", n, err)
	}
	if _, _, ok := claimRefresh(e); ok {
		t.Fatal("parked refresh claimable before the debounce")
	}
	over := &domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "be brief"}}
	op, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{
		Bank: e.bank, ID: "status", ForceFull: true, Overrides: over, Strategy: domain.RefreshStrategyAgentic,
	})
	if err != nil || !joined {
		t.Fatalf("manual refresh joined=%v err=%v", joined, err)
	}
	assertUpgraded(t, e, op.ID)
	op2, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{Bank: e.bank, ID: "status"})
	if err != nil || joined || op2.ID == op.ID {
		t.Fatalf("a refresh behind a running one must queue anew: joined=%v err=%v", joined, err)
	}
}

// assertUpgraded claims the refresh and checks it is op, released and upgraded by the manual join.
func assertUpgraded(t *testing.T, e *env, op domain.OperationID) {
	t.Helper()
	job, p, ok := claimRefresh(e)
	if !ok || job.ID != op {
		t.Fatalf("manual join did not release the parked refresh (claimed %v)", ok)
	}
	if !p.ForceFull || !p.Manual || p.Strategy != domain.RefreshStrategyAgentic || job.Overrides == nil ||
		job.Overrides.GetReflect().Mission != "be brief" {
		t.Fatalf("not upgraded: %+v overrides %+v", p, job.Overrides)
	}
}

// TestPauseAndManualUnpause checks that a recorded failure pauses automatic refreshes, the record is
// idempotent, an automatic success does not unpause, and only a manual success does.
func TestPauseAndManualUnpause(t *testing.T) {
	e := newEnv(t)
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Schedule.AfterConsolidation = true
	e.addModel(spec)
	e.addUnit(domain.FactTypeWorld, "Alice leads", "proj")
	e.maint.failed = []store.FailedRefresh{{
		Bank: e.bank, ModelID: "status", OperationID: opID("op1"), FailedAt: e.clock.Now(),
		Error: "mentalmodel: delta: " + errAllOpsRejected.Error(),
	}}
	for i, want := range []int{1, 0} {
		if n, err := e.svc.ReconcileFailedRefreshes(e.ctx); err != nil || n != want {
			t.Fatalf("reconcile %d paused %d (want %d): %v", i, n, want, err)
		}
	}
	m := e.model("status")
	h := e.history("status", domain.HistoryKindRefreshFailed)
	if !m.AutoRefreshPaused || m.LastRefreshFailedAt == nil || len(h) != 1 ||
		h[0].FailureReason != failureDeltaOpsAllSkipped {
		t.Fatalf("paused %v history %+v", m.AutoRefreshPaused, h)
	}
	if n, _ := autoTrigger(e, "status"); n != 0 {
		t.Fatal("automatic trigger enqueued a paused model")
	}
	e.clock.Advance(time.Minute)
	e.scriptSynthesize("## Status\n\nAlice leads.")
	if _, err := e.svc.refresh(e.ctx, e.bank, "status", refreshOptions{}); err != nil {
		t.Fatal(err)
	}
	if !e.model("status").AutoRefreshPaused {
		t.Fatal("an automatic success unpaused the model")
	}
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.scriptSynthesize("## Status\n\nAlice leads; Bob joined.")
	if _, err := e.svc.refresh(e.ctx, e.bank, "status", refreshOptions{Manual: true, ForceFull: true}); err != nil {
		t.Fatal(err)
	}
	if e.model("status").AutoRefreshPaused {
		t.Fatal("a manual success did not unpause the model")
	}
	if n, _ := e.svc.ReconcileFailedRefreshes(e.ctx); n != 0 {
		t.Fatal("a failure older than the last success was recorded again")
	}
}
