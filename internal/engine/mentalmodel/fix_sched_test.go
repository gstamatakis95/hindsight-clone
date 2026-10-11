package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
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
			tr, _ := newTriggers(e)
			spec := deltaSpec("status", domain.RefreshStrategySingleShot)
			spec.Trigger.RefreshCron = "0 * * * *"
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
			e.reads.cron = []store.CronModel{cm}
			now := fire.Add(c.tick)
			e.clock.Set(now)
			if n, err := tr.EnqueueDueCronRefreshes(e.ctx, now); err != nil || n != c.want {
				t.Fatalf("inserted %d (want %d): %v", n, c.want, err)
			}
		})
	}
}

// TestParseCronStrict checks that refresh_cron takes exactly 5 UTC fields: no @ descriptors, no time zone
// prefix and no seconds field, both in the parser and in spec validation.
func TestParseCronStrict(t *testing.T) {
	for _, expr := range []string{"@hourly", "@every 1h", "TZ=UTC 0 * * * *", "CRON_TZ=UTC 0 * * *", "0 0 * * * *"} {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("%q accepted", expr)
		}
		spec := domain.MentalModelSpec{ID: "m", Trigger: domain.RefreshTrigger{RefreshCron: expr}}
		if _, err := normalizeSpec(spec); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("normalizeSpec(%q): %v", expr, err)
		}
	}
	for _, expr := range []string{"*/5 * * * *", " 0 9 * * 1-5 "} {
		if _, err := cronParser().Parse(expr); err != nil {
			t.Errorf("%q rejected: %v", expr, err)
		}
	}
}

// TestWeakerJoinerKeepsUpgrade checks that once a manual force_full refresh with overrides and a strategy
// joined the queued refresh, a later automatic trigger and a plain manual request join it without
// downgrading force_full, manual, the overrides or the strategy.
func TestWeakerJoinerKeepsUpgrade(t *testing.T) {
	e := newEnv(t)
	tr, adm := newTriggers(e)
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Trigger.RefreshAfterConsolidation = true
	e.addModel(spec)
	e.addUnit(domain.FactTypeWorld, "new", "proj")
	if n, err := autoTrigger(e, tr, "status"); err != nil || n != 1 {
		t.Fatalf("first trigger inserted %d: %v", n, err)
	}
	op, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{
		Bank: e.bank, ID: "status", ForceFull: true, Strategy: domain.RefreshStrategyAgentic,
		Overrides: &domain.BankConfig{ReflectMission: "be brief"},
	})
	if err != nil || !joined {
		t.Fatalf("manual force_full joined=%v err=%v", joined, err)
	}
	if n, err := autoTrigger(e, tr, "status"); err != nil || n != 0 {
		t.Fatalf("automatic joiner inserted %d: %v", n, err)
	}
	op2, joined, err := adm.RefreshMentalModel(e.ctx, RefreshRequest{Bank: e.bank, ID: "status"})
	if err != nil || !joined || op2.ID != op.ID {
		t.Fatalf("plain manual joined=%v same=%v err=%v", joined, op2.ID == op.ID, err)
	}
	assertUpgraded(t, e, op.ID)
}

// recordingRunner is a RefreshRunner that records its calls.
type recordingRunner struct {
	calls []runCall
}

// runCall is one recorded RefreshRunner.Run call.
type runCall struct {
	b    domain.BankRef
	id   domain.MentalModelID
	opts RefreshOptions
}

// Run implements RefreshRunner.
func (r *recordingRunner) Run(
	_ context.Context, b domain.BankRef, id domain.MentalModelID, opts RefreshOptions,
) (RefreshOutcome, error) {
	r.calls = append(r.calls, runCall{b: b, id: id, opts: opts})
	return RefreshOutcome{}, nil
}

// TestRunJobErrorKindAndOptions checks that a malformed payload is domain.ErrInvalidArgument and never runs,
// and that the payload flags, the job's overrides and its operation id reach the runner.
func TestRunJobErrorKindAndOptions(t *testing.T) {
	e := newEnv(t)
	r := &recordingRunner{}
	for _, raw := range []string{`not json`, `{}`, `{"mental_model_id":""}`} {
		_, err := RunJob(e.ctx, r, store.Job{Bank: e.bank, Payload: json.RawMessage(raw)})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("payload %s: %v", raw, err)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("a malformed payload ran: %+v", r.calls)
	}
	payload, _ := json.Marshal(RefreshPayload{
		MentalModelID: "status", ForceFull: true, Manual: true, Strategy: domain.RefreshStrategyAgentic,
	})
	over := &domain.BankConfig{ReflectMission: "be brief"}
	job := store.Job{ID: opID("op7"), Bank: e.bank, Payload: payload, Overrides: over}
	if _, err := RunJob(e.ctx, r, job); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("ran %d times", len(r.calls))
	}
	c := r.calls[0]
	o := c.opts
	if c.b != e.bank || c.id != "status" || !o.Manual || !o.ForceFull || o.DryRun ||
		o.Strategy != domain.RefreshStrategyAgentic || o.Overrides != over || o.OperationID != opID("op7") {
		t.Fatalf("options not passed through: %+v", c)
	}
}

// downEmbedder is a ai.Embedder that always fails.
type downEmbedder struct{}

// EmbedDocuments implements ai.Embedder.
func (downEmbedder) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embedder down")
}

// EmbedQuery implements ai.Embedder.
func (downEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, errors.New("embedder down")
}

// TestSearchPagesEmbedderDown checks that SearchPages ranks by terms only when the embedder fails.
func TestSearchPagesEmbedderDown(t *testing.T) {
	e := newEnv(t)
	for id, content := range map[domain.MentalModelID]string{
		"deploy": "# Deploy\n\nWe deploy with Argo every Tuesday.",
		"hiring": "# Hiring\n\nInterviews take two rounds.",
	} {
		e.addModel(domain.MentalModelSpec{ID: id, Name: string(id)})
		m := e.model(id)
		m.ContentMD = content
		e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	}
	hits, err := NewPageSearcher(e.reads, downEmbedder{}).Search(e.ctx, SearchPagesRequest{
		Bank: e.bank, Query: "how do we deploy?",
	})
	if err != nil || len(hits) != 1 || hits[0].MentalModelID != "deploy" {
		t.Fatalf("hits %+v: %v", hits, err)
	}
}
