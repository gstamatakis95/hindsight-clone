package mentalmodel

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"strings"
	"testing"
	"time"
)

// cronCase is one EnqueueDueCronRefreshes scenario on an hourly model whose first fire time is F.
type cronCase struct {
	name    string
	tick    time.Duration // now = F + tick
	served  time.Duration // LastRefreshEnqueuedAt = F + served; ignored when unserved
	unsrv   bool          // LastRefreshEnqueuedAt is zero
	created time.Duration // CreatedAt = F + created
	paused  bool
	fresh   bool // no in-scope write since the model was built
	want    int
}

// TestCronFiresOnScheduleWithinGrace checks that a cron refresh is due only within cronGrace of a fire time
// that no refresh enqueue has served yet and that is not before the model's creation; paused and fresh
// models are skipped.
func TestCronFiresOnScheduleWithinGrace(t *testing.T) {
	cases := []cronCase{
		{name: "before the fire time", tick: -time.Second, unsrv: true, created: -time.Hour, want: 0},
		{name: "at the fire time", unsrv: true, created: -time.Hour, want: 1},
		{name: "just after the fire time", tick: 2 * time.Minute, unsrv: true, created: -time.Hour, want: 1},
		{name: "outside the grace window", tick: 30 * time.Minute, unsrv: true, created: -time.Hour, want: 0},
		{name: "missed fire hours ago", tick: 2*time.Hour + 30*time.Minute, unsrv: true, created: -time.Hour},
		{name: "enqueued before the fire time", tick: time.Minute, served: -time.Second, created: -time.Hour, want: 1},
		{name: "served at the fire time", tick: time.Minute, served: 0, created: -time.Hour, want: 0},
		{name: "served after the fire time", tick: 2 * time.Minute, served: time.Minute, created: -time.Hour},
		{name: "created after the fire time", tick: 2 * time.Minute, unsrv: true, created: time.Second},
		{name: "paused", unsrv: true, created: -time.Hour, paused: true, want: 0},
		{name: "fresh", unsrv: true, created: -time.Hour, fresh: true, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			spec := deltaSpec("status", domain.RefreshStrategySingleShot)
			spec.Schedule.Cron = "0 * * * *"
			m := e.addModel(spec)
			if !c.fresh {
				e.addUnit(domain.FactTypeWorld, "new", "proj")
			}
			fire := e.clock.Now().Truncate(time.Hour).Add(time.Hour)
			cm := store.CronModel{Model: m}
			cm.Model.CreatedAt, cm.Model.AutoRefreshPaused = fire.Add(c.created), c.paused
			if !c.unsrv {
				cm.LastRefreshEnqueuedAt = fire.Add(c.served)
			}
			e.maint.cron = []store.CronModel{cm}
			now := fire.Add(c.tick)
			e.clock.Set(now)
			if n, err := e.svc.EnqueueDueCronRefreshes(e.ctx); err != nil || n != c.want {
				t.Fatalf("inserted %d (want %d): %v", n, c.want, err)
			}
		})
	}
}

// TestParseCronStrict checks that schedule.cron takes exactly 5 UTC fields: no @ descriptors, no time zone
// prefix and no seconds field, both in the parser and in spec validation.
func TestParseCronStrict(t *testing.T) {
	for _, expr := range []string{"@hourly", "@every 1h", "TZ=UTC 0 * * * *", "CRON_TZ=UTC 0 * * *", "0 0 * * * *"} {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("%q accepted", expr)
		}
		spec := domain.MentalModelSpec{ID: "m", Schedule: domain.RefreshSchedule{Cron: expr}}
		if _, err := normalizeSpec(spec); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("normalizeSpec(%q): %v", expr, err)
		}
	}
	for _, expr := range []string{"*/5 * * * *", " 0 9 * * 1-5 "} {
		if _, err := parseCron(expr); err != nil {
			t.Errorf("%q rejected: %v", expr, err)
		}
	}
}

// TestWeakerJoinerKeepsUpgrade checks that once a manual force_full refresh with overrides and a strategy
// joined the queued refresh, a later automatic trigger and a plain manual request join it without
// downgrading force_full, manual, the overrides or the strategy.
func TestWeakerJoinerKeepsUpgrade(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Schedule.AfterConsolidation = true
	e.addModel(spec)
	e.addUnit(domain.FactTypeWorld, "new", "proj")
	if n, err := autoTrigger(e, "status"); err != nil || n != 1 {
		t.Fatalf("first trigger inserted %d: %v", n, err)
	}
	op, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{
		Bank: e.bank, ID: "status", ForceFull: true, Strategy: domain.RefreshStrategyAgentic,
		Overrides: &domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "be brief"}},
	})
	if err != nil || !joined {
		t.Fatalf("manual force_full joined=%v err=%v", joined, err)
	}
	if n, err := autoTrigger(e, "status"); err != nil || n != 0 {
		t.Fatalf("automatic joiner inserted %d: %v", n, err)
	}
	op2, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{Bank: e.bank, ID: "status"})
	if err != nil || !joined || op2.ID != op.ID {
		t.Fatalf("plain manual joined=%v same=%v err=%v", joined, op2.ID == op.ID, err)
	}
	assertUpgraded(t, e, op.ID)
}

// TestRunRefreshJob checks the refresh_mental_model handler: a malformed payload is domain.ErrInvalidArgument, a
// deleted model's job is done, and a run passes the payload's flags and the job's overrides through and records
// the result keys GetOperation shows (part of the API) with the run's usage.
func TestRunRefreshJob(t *testing.T) {
	e := newEnv(t)
	for _, raw := range []string{`not json`, `{}`, `{"mental_model_id":""}`} {
		run := jobs.Inline(store.Job{Bank: e.bank, Kind: domain.OperationKindRefreshMentalModel, Payload: []byte(raw)})
		if err := e.svc.RunRefreshJob(e.ctx, run); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("payload %s: %v", raw, err)
		}
	}
	gone, _ := json.Marshal(jobs.RefreshPayload{MentalModelID: "gone"})
	run := jobs.Inline(store.Job{Bank: e.bank, Kind: domain.OperationKindRefreshMentalModel, Payload: gone})
	if err := e.svc.RunRefreshJob(e.ctx, run); err != nil {
		t.Fatalf("deleted model: %v", err)
	}

	m, _ := builtModel(e, domain.RefreshStrategySingleShot)
	m.AutoRefreshPaused = true
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.scriptSynthesize("## Status\n\nAlice leads, briefly.")
	payload, _ := json.Marshal(jobs.RefreshPayload{
		MentalModelID: "status", ForceFull: true, Manual: true, Strategy: domain.RefreshStrategySingleShot,
	})
	run = jobs.Inline(store.Job{
		ID: opID("op7"), Bank: e.bank, Kind: domain.OperationKindRefreshMentalModel, Payload: payload,
		Overrides: &domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "be brief"}},
	})
	if err := e.svc.RunRefreshJob(e.ctx, run); err != nil {
		t.Fatal(err)
	}
	result, usage := run.Result()
	want := `{"effective_mode":"full","effective_strategy":"single_shot","facts_used":1,` +
		`"outcome":"content_written","version":2}`
	if string(result) != want || len(usage) != 1 || usage[0].Slot != domain.ModelSlotMentalModel {
		t.Fatalf("result %s usage %+v, want %s", result, usage, want)
	}
	calls := e.llm.CallsFor(domain.ModelSlotMentalModel, prompts.SynthesizeSystem)
	if len(calls) != 1 || !strings.Contains(calls[0].Chat.Messages[0].Content, "be brief") {
		t.Fatal("the job's overrides did not reach the synthesize prompt")
	}
	if got := e.model("status"); got.AutoRefreshPaused || got.Version != 2 {
		t.Fatalf("manual job run: paused %v version %d", got.AutoRefreshPaused, got.Version)
	}
}
