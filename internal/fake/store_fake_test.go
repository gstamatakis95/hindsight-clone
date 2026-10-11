package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"testing"
	"time"
)

// Tests of fake-specific behaviour and of the carried-forward contract items the porttest suites don't
// cover yet (tenant isolation on every write port, Fail on cancel-requested and tombstoned-bank rows).

var (
	mine   = port.BankRef{Tenant: "t1", Bank: "b"}
	theirs = port.BankRef{Tenant: "t2", Bank: "b"}
)

// fixture is a store on a frozen clock with two same-id banks under different tenants.
type fixture struct {
	t     *testing.T
	clock *fake.Clock
	s     *fake.Store
}

// newFixture returns a fixture with both banks ensured.
func newFixture(t *testing.T) fixture {
	t.Helper()
	clock := fake.NewClock(time.Time{})
	f := fixture{t: t, clock: clock, s: fake.NewStore(clock)}
	f.tx(func(tx port.Tx) error {
		for _, b := range []port.BankRef{mine, theirs} {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
		}
		return nil
	})
	return f
}

// tx runs fn in a transaction and fails the test on error.
func (f fixture) tx(fn func(tx port.Tx) error) {
	f.t.Helper()
	if err := f.s.InTx(f.t.Context(), fn); err != nil {
		f.t.Fatalf("InTx: %v", err)
	}
}

// txErr runs fn in its own transaction and returns its error.
func (f fixture) txErr(fn func(tx port.Tx) error) error {
	return f.s.InTx(f.t.Context(), fn) //nolint:wrapcheck // tests assert on the store's error as returned
}

// unit inserts a world fact of b with an embedding and returns it.
func (f fixture) unit(b port.BankRef, id port.UnitID, doc port.DocumentID) domain.Unit {
	f.t.Helper()
	u := domain.Unit{ID: id, Bank: b, Type: domain.FactTypeWorld, Text: "fact " + string(id), DocumentID: doc}
	f.tx(func(tx port.Tx) error {
		if doc != "" {
			if err := tx.Documents().Upsert(f.t.Context(), domain.Document{ID: doc, Bank: b}); err != nil {
				return err
			}
		}
		seq, err := tx.Seq(f.t.Context(), b)
		if err != nil {
			return err
		}
		u.Seq = seq
		return tx.Units().Insert(f.t.Context(), []domain.Unit{u})
	})
	return u
}

// enqueue enqueues j and returns its id.
func (f fixture) enqueue(j port.Job) port.OperationID {
	f.t.Helper()
	var id port.OperationID
	f.tx(func(tx port.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(f.t.Context(), j)
		return err
	})
	return id
}

// claim claims every due job of kind and fails unless exactly want come back.
func (f fixture) claim(kind port.JobKind, want int) []port.Job {
	f.t.Helper()
	js, err := f.s.Claim(f.t.Context(), "w", []port.JobKind{kind}, 100)
	if err != nil || len(js) != want {
		f.t.Fatalf("Claim(%s) = %d jobs, %v; want %d", kind, len(js), err, want)
	}
	return js
}

// status returns the status of operation id of b.
func (f fixture) status(b port.BankRef, id port.OperationID) domain.OperationStatus {
	f.t.Helper()
	var op domain.Operation
	f.tx(func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(f.t.Context(), b, id)
		return err
	})
	return op.Status
}

// events lists every event of b.
func (f fixture) events(b port.BankRef) []domain.LifecycleEvent {
	f.t.Helper()
	var evs []domain.LifecycleEvent
	f.tx(func(tx port.Tx) error {
		var err error
		evs, _, err = tx.Events().List(f.t.Context(), b, 0, 0, nil)
		return err
	})
	return evs
}

// TestFailWithRetryOnCancelRequested: Fail(cause, retryAt) on a cancel-requested row ends CANCELLED with
// ErrCancelled, not PENDING.
func TestFailWithRetryOnCancelRequested(t *testing.T) {
	f := newFixture(t)
	id := f.enqueue(port.Job{Bank: mine, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1"})
	f.claim(domain.OperationKindRetainDocument, 1)
	f.tx(func(tx port.Tx) error { _, err := tx.Queue().Cancel(t.Context(), mine, id); return err })
	retry := f.clock.Now().Add(-time.Minute)
	err := f.s.Fail(t.Context(), id, errors.New("transient"), &retry)
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Fail(retry) on a cancel-requested row = %v, want ErrCancelled", err)
	}
	if got := f.status(mine, id); got != domain.OperationStatusCancelled {
		t.Errorf("status = %q, want cancelled", got)
	}
	f.claim(domain.OperationKindRetainDocument, 0)
}

// TestFailOnTombstonedBank: a terminal Fail on a tombstoned bank goes FAILED without an event or seq bump;
// a reclaim past max attempts does the same.
func TestFailOnTombstonedBank(t *testing.T) {
	f := newFixture(t)
	failed := f.enqueue(port.Job{Bank: mine, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate"})
	stale := f.enqueue(port.Job{Bank: mine, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1"})
	f.claim(domain.OperationKindConsolidate, 1)
	retry := f.clock.Now().Add(-time.Minute)
	for range domain.OperationKindRetainDocument.MaxAttempts() - 1 { // bring stale to its last attempt
		f.claim(domain.OperationKindRetainDocument, 1)
		if err := f.s.Fail(t.Context(), stale, errors.New("transient"), &retry); err != nil {
			t.Fatalf("Fail(retry): %v", err)
		}
	}
	last := domain.OperationKindRetainDocument.MaxAttempts() - 1
	if js := f.claim(domain.OperationKindRetainDocument, 1); js[0].Attempt != last {
		t.Fatalf("Attempt = %d, want the last one", js[0].Attempt)
	}
	f.tx(func(tx port.Tx) error { return tx.Banks().MarkDeleting(t.Context(), mine, f.clock.Now()) })
	seq, _ := f.s.Read().BankSeq(t.Context(), mine)
	if err := f.s.Fail(t.Context(), failed, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	f.clock.Advance(time.Hour)
	if n, err := f.s.ReclaimStale(t.Context(), time.Minute); err != nil || n != 1 {
		t.Fatalf("ReclaimStale = %d, %v; want 1", n, err)
	}
	for _, id := range []port.OperationID{failed, stale} {
		if got := f.status(mine, id); got != domain.OperationStatusFailed {
			t.Errorf("status of %s = %q, want failed", id, got)
		}
	}
	if evs := f.events(mine); len(evs) != 0 {
		t.Errorf("events on a tombstoned bank = %+v, want none", evs)
	}
	if got, _ := f.s.Read().BankSeq(t.Context(), mine); got != seq {
		t.Errorf("BankSeq = %d, want unchanged %d", got, seq)
	}
}

// TestTenantIsolationWritePorts: every write port that names a bank (or an id owned by one) leaves the
// same bank id under another tenant untouched.
func TestTenantIsolationWritePorts(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	u := f.unit(theirs, "u-theirs", "doc-1")
	other := domain.Unit{ID: "o-mine", Bank: mine, Type: domain.FactTypeObservation, Text: "observation"}
	f.tx(func(tx port.Tx) error { return tx.Units().Insert(ctx, []domain.Unit{other}) })
	checks := map[string]func(tx port.Tx) error{
		"Units.Update": func(tx port.Tx) error {
			v := u
			v.Bank = mine
			return tx.Units().Update(ctx, v)
		},
		"Units.Archive": func(tx port.Tx) error { return tx.Units().Archive(ctx, mine, u.ID, "x") },
		"Units.Restore": func(tx port.Tx) error { return tx.Units().Restore(ctx, mine, u.ID) },
		"Units.UpsertObservation": func(tx port.Tx) error {
			v := u
			v.Bank, v.Type = mine, domain.FactTypeObservation
			return tx.Units().UpsertObservation(ctx, v)
		},
		"Units.RecordObservationVersion": func(tx port.Tx) error {
			return tx.Units().RecordObservationVersion(ctx, mine, u.ID, domain.ObservationVersion{Version: 1})
		},
		"Documents.Complete": func(tx port.Tx) error { return tx.Documents().Complete(ctx, mine, "doc-1", "h", "o") },
		"Documents.SetTags":  func(tx port.Tx) error { return tx.Documents().SetTags(ctx, mine, "doc-1", []string{"x"}) },
		"Links.Insert": func(tx port.Tx) error {
			return tx.Links().Insert(ctx, []domain.Link{{Bank: mine, From: other.ID, To: u.ID, Kind: domain.LinkSemantic}})
		},
		"Evidence.Add": func(tx port.Tx) error {
			return tx.Evidence().Add(ctx, other.ID, []domain.Evidence{{Unit: u.ID}})
		},
		"MentalModels.Update": func(tx port.Tx) error {
			return tx.MentalModels().Update(ctx, domain.MentalModel{Bank: mine, Spec: domain.MentalModelSpec{ID: "m"}})
		},
		"MentalModels.Delete": func(tx port.Tx) error { return tx.MentalModels().Delete(ctx, mine, "m") },
		"MentalModels.AppendHistory": func(tx port.Tx) error {
			return tx.MentalModels().AppendHistory(ctx, mine, "m", domain.MentalModelHistoryEntry{}, 0)
		},
		"Directives.Delete": func(tx port.Tx) error { return tx.Directives().Delete(ctx, mine, "d") },
	}
	f.tx(func(tx port.Tx) error {
		m := domain.MentalModel{Bank: theirs, Spec: domain.MentalModelSpec{ID: "m"}}
		if err := tx.MentalModels().Create(ctx, m); err != nil {
			return err
		}
		_, err := tx.Directives().Upsert(ctx, domain.Directive{ID: "d", Bank: theirs, Name: "d"})
		return err
	})
	for name, write := range checks {
		if err := f.txErr(write); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s across tenants = %v, want ErrNotFound", name, err)
		}
	}
	f.checkTheirsUntouched(u)
}

// checkTheirsUntouched checks that the other tenant's unit, document, model and directive are unchanged.
func (f fixture) checkTheirsUntouched(u domain.Unit) {
	f.t.Helper()
	ctx := f.t.Context()
	us, err := f.s.Read().GetUnits(ctx, theirs, []port.UnitID{u.ID})
	if err != nil || len(us) != 1 || us[0].Text != u.Text {
		f.t.Errorf("their unit = %+v, %v; want it unchanged", us, err)
	}
	f.tx(func(tx port.Tx) error {
		d, err := tx.Documents().Get(ctx, theirs, "doc-1")
		if err != nil || d.ContentHash != "" || len(d.Tags) != 0 {
			f.t.Errorf("their document = %+v, %v; want it unchanged", d, err)
		}
		if _, err := tx.MentalModels().Get(ctx, theirs, "m"); err != nil {
			f.t.Errorf("their mental model: %v", err)
		}
		ds, err := tx.Directives().List(ctx, theirs, false)
		if err != nil || len(ds) != 1 {
			f.t.Errorf("their directives = %+v, %v; want one", ds, err)
		}
		return nil
	})
}

// TestTenantIsolationBankWrites: bank-scoped bulk writes (SetDocumentTags, DeleteByDocument, DeleteCiting,
// ClearDirty, PruneOrphans, CancelBank, Queue Cancel/Retry) of one tenant change nothing of the other's.
func TestTenantIsolationBankWrites(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	u := f.unit(theirs, "u-theirs", "doc-1")
	var ent port.EntityID
	f.tx(func(tx port.Tx) error {
		var err error
		if ent, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: theirs, CanonicalName: "Ann"}); err != nil {
			return err
		}
		return tx.Entities().Detach(ctx, u.ID) // nothing attached; queues nothing
	})
	op := f.enqueue(port.Job{Bank: theirs, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate"})
	f.myBankWrites(ent, u.ID)
	for name, call := range map[string]func(port.Queue) error{
		"Cancel": func(q port.Queue) error { _, err := q.Cancel(ctx, mine, op); return err },
		"Retry":  func(q port.Queue) error { _, err := q.Retry(ctx, mine, op); return err },
	} {
		if err := f.txErr(func(tx port.Tx) error { return call(tx.Queue()) }); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Queue.%s(mine, their op) = %v, want ErrNotFound", name, err)
		}
	}
	if got := f.status(theirs, op); got != domain.OperationStatusPending {
		t.Errorf("their op = %q, want pending", got)
	}
	us, _ := f.s.Read().GetUnits(ctx, theirs, []port.UnitID{u.ID})
	if len(us) != 1 || len(us[0].Tags) != 0 {
		t.Errorf("their unit after my bank writes = %+v, want it untouched", us)
	}
}

// TestFrozenClockDrivesQueue: with a *Clock, run_at due-ness and heartbeat staleness follow Advance.
func TestFrozenClockDrivesQueue(t *testing.T) {
	f := newFixture(t)
	later := f.clock.Now().Add(10 * time.Minute)
	id := f.enqueue(port.Job{Bank: mine, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:a", RunAt: later})
	f.claim(domain.OperationKindRefreshMentalModel, 0)
	f.clock.Advance(10 * time.Minute)
	f.claim(domain.OperationKindRefreshMentalModel, 1)
	if n, err := f.s.ReclaimStale(t.Context(), time.Minute); err != nil || n != 0 {
		t.Fatalf("ReclaimStale before the ttl = %d, %v; want 0", n, err)
	}
	f.clock.Advance(2 * time.Minute)
	if n, err := f.s.ReclaimStale(t.Context(), time.Minute); err != nil || n != 1 {
		t.Fatalf("ReclaimStale after the ttl = %d, %v; want 1", n, err)
	}
	if got := f.status(mine, id); got != domain.OperationStatusPending {
		t.Errorf("status after reclaim = %q, want pending", got)
	}
}

// TestEventAppendNeedsTxSeq: an event not stamped with the transaction's Seq is rejected.
func TestEventAppendNeedsTxSeq(t *testing.T) {
	f := newFixture(t)
	ev := domain.LifecycleEvent{Bank: mine, Type: domain.EventTypeMemoryCurated, Seq: 7}
	if err := f.txErr(func(tx port.Tx) error { return tx.Events().Append(t.Context(), ev) }); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Errorf("Append without Tx.Seq = %v, want ErrInvalidArgument", err)
	}
}

// TestInTxHonoursContext: a transaction waiting for the store lock gives up when its context ends.
func TestInTxHonoursContext(t *testing.T) {
	f := newFixture(t)
	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.s.InTx(context.Background(), func(port.Tx) error { close(held); <-release; return nil })
	}()
	<-held
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := f.s.InTx(ctx, func(port.Tx) error { return nil })
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("InTx while the lock is held = %v, want DeadlineExceeded", err)
	}
	if err := <-done; err != nil {
		t.Errorf("holder InTx: %v", err)
	}
}

// TestCompleteCancelledCommits: Complete of a cancel-requested job commits the CANCELLED status even
// though it returns ErrCancelled, and stores no result.
func TestCompleteCancelledCommits(t *testing.T) {
	f := newFixture(t)
	id := f.enqueue(port.Job{Bank: mine, Kind: domain.OperationKindGraphMaintenance, SerialKey: "graph"})
	f.claim(domain.OperationKindGraphMaintenance, 1)
	f.tx(func(tx port.Tx) error { _, err := tx.Queue().Cancel(t.Context(), mine, id); return err })
	if err := f.s.Complete(t.Context(), id, json.RawMessage(`{"n":1}`), nil); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Complete = %v, want ErrCancelled", err)
	}
	if got := f.status(mine, id); got != domain.OperationStatusCancelled {
		t.Errorf("status = %q, want cancelled", got)
	}
}

// myBankWrites runs mine's bank-scoped bulk writes against ids and names owned by theirs and checks that
// each touches nothing.
func (f fixture) myBankWrites(ent port.EntityID, unit port.UnitID) {
	f.t.Helper()
	t, ctx := f.t, f.t.Context()
	f.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(ctx, mine); err != nil {
			return err
		}
		if ids, err := tx.Units().SetDocumentTags(ctx, mine, "doc-1", nil, []string{"x"}, 1); err != nil || len(ids) != 0 {
			t.Errorf("SetDocumentTags(mine) = %v, %v; want nothing", ids, err)
		}
		if ids, err := tx.Units().DeleteByDocument(ctx, mine, "doc-1"); err != nil || len(ids) != 0 {
			t.Errorf("DeleteByDocument(mine) = %v, %v; want nothing", ids, err)
		}
		if n, err := tx.Entities().PruneOrphans(ctx, mine, []port.EntityID{ent}); err != nil || n != 0 {
			t.Errorf("PruneOrphans(mine, their entity) = %d, %v; want 0", n, err)
		}
		if n, err := tx.Queue().CancelBank(ctx, mine); err != nil || n != 0 {
			t.Errorf("CancelBank(mine) = %d, %v; want 0", n, err)
		}
		if _, _, err := tx.Evidence().DeleteCiting(ctx, mine, []port.UnitID{unit}); err != nil {
			t.Errorf("DeleteCiting(mine): %v", err)
		}
		if err := tx.Links().ClearDirty(ctx, mine, []port.UnitID{unit}); err != nil {
			t.Errorf("ClearDirty(mine): %v", err)
		}
		_, err := tx.Entities().Upsert(ctx, domain.Entity{ID: ent, Bank: mine, CanonicalName: "Hijack"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Entities.Upsert(mine, their id) = %v, want ErrNotFound", err)
		}
		return nil
	})
}
