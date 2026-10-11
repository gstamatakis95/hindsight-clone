package mentalmodel

import (
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
	"time"
)

// TestAutomaticRefreshRunsAndStaysUnpaused queues an automatic refresh the way consolidation does (tx.Seq, then
// jobs.EnqueueRefresh parked for the debounce), claims and runs it through RunRefreshJob, completes it, and checks
// that the failed-refresh reconcile over the store's own FailedRefreshes pauses nothing.
func TestAutomaticRefreshRunsAndStaysUnpaused(t *testing.T) {
	e := newEnv(t)
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Mode = domain.RefreshModeFull
	e.addModel(spec)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	e.tx(func(tx store.Tx) error {
		if _, err := tx.Seq(e.ctx, e.bank); err != nil {
			return err
		}
		_, _, err := jobs.EnqueueRefresh(e.ctx, tx, jobs.RefreshRequest{
			Bank: e.bank, ID: "status", RunAt: e.clock.Now().Add(30 * time.Second),
		}, e.clock.Now())
		return err //nolint:wrapcheck // test helper; e.tx fails the test with it
	})

	e.clock.Advance(time.Minute) // past the refresh debounce
	claimed := e.claimDue(domain.OperationKindRefreshMentalModel)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d refresh jobs, want 1", len(claimed))
	}
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	runErr := e.svc.RunRefreshJob(e.ctx, jobs.Inline(claimed[0]))
	if runErr != nil {
		if err := e.store.Fail(e.ctx, claimed[0].ID, runErr, nil); err != nil {
			t.Fatal(err)
		}
	} else if err := e.store.Complete(e.ctx, claimed[0].ID, nil, nil); err != nil {
		t.Fatal(err)
	}

	paused, err := e.service(func(d *Deps) { d.Maintenance = e.store }).ReconcileFailedRefreshes(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	if runErr != nil || paused != 0 || m.AutoRefreshPaused || m.Version != 1 {
		t.Fatalf("refresh err %v, paused %d (model paused %v), version %d", runErr, paused, m.AutoRefreshPaused,
			m.Version)
	}
}
