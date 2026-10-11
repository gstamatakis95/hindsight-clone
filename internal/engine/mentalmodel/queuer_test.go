package mentalmodel

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// queue runs QueueRefresh in its own transaction (taking tx.Seq first, as callers must).
func queue(e *env, q RefreshQueuer, id port.MentalModelID) port.OperationID {
	e.t.Helper()
	var op port.OperationID
	e.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(e.ctx, e.bank); err != nil {
			return err
		}
		var err error
		op, err = q.QueueRefresh(e.ctx, tx, e.bank, id)
		return err //nolint:wrapcheck // test helper; e.tx fails the test with it
	})
	return op
}

// TestRefreshQueuer checks the in-transaction queuer: a never-built model is due now and full, a built
// one is parked for the debounce and joined by a second call, and a paused or deleted model is skipped.
func TestRefreshQueuer(t *testing.T) {
	e := newEnv(t)
	q := NewRefreshQueuer(e.clock)
	e.addModel(domain.MentalModelSpec{ID: "fresh"})
	op := queue(e, q, "fresh")
	job, p, ok := claimRefresh(e)
	if op == "" || !ok || job.ID != op || !p.ForceFull {
		t.Fatalf("first build not due now: op %q claimed %v payload %+v", op, ok, p)
	}

	m, _ := builtModel(e, domain.RefreshStrategySingleShot)
	refreshed := e.clock.Now()
	m.LastRefreshedAt = &refreshed
	e.tx(func(tx port.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	op1, op2 := queue(e, q, "status"), queue(e, q, "status")
	if op1 == "" || op1 != op2 {
		t.Fatalf("second queue did not join: %q %q", op1, op2)
	}
	if _, _, ok := claimRefresh(e); ok {
		t.Fatal("refresh of a built model not parked for the debounce")
	}
	e.clock.Advance(31 * time.Second)
	if job, _, ok := claimRefresh(e); !ok || job.ID != op1 {
		t.Fatal("parked refresh not due after the debounce")
	}

	m = e.model("status")
	m.AutoRefreshPaused = true
	e.tx(func(tx port.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	if op := queue(e, q, "status"); op != "" {
		t.Fatalf("paused model queued %q", op)
	}
	if op := queue(e, q, "gone"); op != "" {
		t.Fatalf("deleted model queued %q", op)
	}
}

// TestQueuerNeverBuiltIsLastRefreshedAtNil checks that a model whose first build ran but found nothing
// (version 0, no content, last_refreshed_at set) counts as built: its refresh is parked, not forced full.
func TestQueuerNeverBuiltIsLastRefreshedAtNil(t *testing.T) {
	e := newEnv(t)
	q := NewRefreshQueuer(e.clock)
	e.addModel(domain.MentalModelSpec{ID: "empty"})
	if _, err := e.runner.Run(e.ctx, e.bank, "empty", RefreshOptions{Manual: true}); err != nil {
		t.Fatal(err)
	}
	if m := e.model("empty"); m.LastRefreshedAt == nil || m.Version != 0 || m.ContentMD != "" {
		t.Fatalf("first build: %+v", m)
	}
	if op := queue(e, q, "empty"); op == "" {
		t.Fatal("not queued")
	}
	if job, p, ok := claimRefresh(e); ok {
		t.Fatalf("refresh of a model built once not parked: %+v %+v", job, p)
	}
}

// emptySynth is a full-only Synthesizer whose retrieval finds nothing.
type emptySynth struct{}

func (emptySynth) Strategy() domain.RefreshStrategy { return domain.RefreshStrategySingleShot }

func (emptySynth) Full(context.Context, domain.MentalModel, domain.BankConfig, int64) (draft, error) {
	return draft{}, ErrNoNewFacts
}

func (emptySynth) Delta(_ context.Context, m domain.MentalModel, _ domain.BankConfig, _ int64) (deltaDraft, error) {
	return deltaDraft{ContentMD: m.ContentMD}, ErrNoNewFacts
}

// TestFullWithNothingDraftedKeepsLiveSources checks that a full run that drafts nothing keeps the content
// and its live sources, and drops only the sources that are gone.
func TestFullWithNothingDraftedKeepsLiveSources(t *testing.T) {
	e := newEnv(t)
	_, u := builtModel(e, domain.RefreshStrategySingleShot)
	runner := NewRefreshRunner(RefreshRunnerDeps{
		Store: e.store, Config: e.config, Scope: e.reads, Clock: e.clock, Synthesizers: []Synthesizer{emptySynth{}},
	})
	if _, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{ForceFull: true}); err != nil {
		t.Fatal(err)
	}
	if m := e.model("status"); !slices.Equal(m.SourceIDs, []port.UnitID{u.ID}) || m.ContentMD == "" {
		t.Fatalf("live source dropped: %v %q", m.SourceIDs, m.ContentMD)
	}
	e.addUnit(domain.FactTypeWorld, "still in scope", "proj")
	e.archive(u.ID)
	if _, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{ForceFull: true}); err != nil {
		t.Fatal(err)
	}
	if m := e.model("status"); len(m.SourceIDs) != 0 || m.ContentMD == "" {
		t.Fatalf("dead source kept: %v", m.SourceIDs)
	}
}

// TestFullWithNothingInScopeKeepsSourcesAndWatermark checks the SPEC 2002 ruling: a run with nothing in
// scope makes no model call and only records that it ran (last_refreshed_at, and a manual run unpauses);
// content, version, source_ids (even a dead one) and last_seq_seen stay.
func TestFullWithNothingInScopeKeepsSourcesAndWatermark(t *testing.T) {
	e := newEnv(t)
	before, u := builtModel(e, domain.RefreshStrategySingleShot)
	before.AutoRefreshPaused = true
	e.tx(func(tx port.Tx) error { return tx.MentalModels().Update(e.ctx, before) })
	e.archive(u.ID)
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true, ForceFull: true})
	if err != nil || out.Trace.Outcome != domain.RefreshOutcomeNoNewFacts || e.llmCalls() != 0 {
		t.Fatalf("run: %v outcome %s llm calls %d", err, out.Trace.Outcome, e.llmCalls())
	}
	m := e.model("status")
	if m.LastRefreshedAt == nil || m.AutoRefreshPaused {
		t.Fatalf("run not recorded: refreshed %v paused %v", m.LastRefreshedAt, m.AutoRefreshPaused)
	}
	if m.ContentMD != before.ContentMD || m.Version != before.Version || m.LastSeqSeen != before.LastSeqSeen ||
		!slices.Equal(m.SourceIDs, before.SourceIDs) {
		t.Fatalf("changed: v%d seq %d (was %d) sources %v", m.Version, m.LastSeqSeen, before.LastSeqSeen, m.SourceIDs)
	}
}
