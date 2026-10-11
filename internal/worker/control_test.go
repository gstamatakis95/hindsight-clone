package worker

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
	"time"
)

// claimOne enqueues and claims one retain job and returns its id.
func claimOne(t *testing.T, ops *opsDB) domain.OperationID {
	t.Helper()
	id := ops.add(retainJob("1"))
	if js, _ := ops.Claim(t.Context(), "w1", []domain.OperationKind{domain.OperationKindRetainDocument}, 1); len(js) != 1 {
		t.Fatal("claim")
	}
	return id
}

func TestCheckpointReport(t *testing.T) {
	t.Run("alive records progress", func(t *testing.T) {
		ops := newOps(t)
		id := claimOne(t, ops)
		if err := NewCheckpoint(ops, id).Report(t.Context(), domain.Progress{Stage: "s", Processed: 1}); err != nil {
			t.Fatalf("Report: %v", err)
		}
		if p := ops.row(id).progress; p == nil || p.Processed != 1 {
			t.Errorf("progress = %+v", p)
		}
	})
	tests := []struct {
		name  string
		setup func(ops *opsDB, id domain.OperationID, cancel context.CancelFunc)
	}{
		{"cancel requested", func(ops *opsDB, id domain.OperationID, _ context.CancelFunc) {
			_, _ = ops.cancel(bankA, id)
		}},
		{"bank tombstoned", func(ops *opsDB, _ domain.OperationID, _ context.CancelFunc) { ops.markDeleting(bankA) }},
		{"row no longer processing", func(ops *opsDB, id domain.OperationID, _ context.CancelFunc) {
			_ = ops.Complete(ops.t.Context(), id, nil, nil)
		}},
		{"row gone", func(ops *opsDB, id domain.OperationID, _ context.CancelFunc) {
			ops.exec(`DELETE FROM operations WHERE id = $1`, string(id))
		}},
		{"context done", func(_ *opsDB, _ domain.OperationID, cancel context.CancelFunc) { cancel() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := newOps(t)
			id := claimOne(t, ops)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tt.setup(ops, id, cancel)
			if err := NewCheckpoint(ops, id).Report(ctx, domain.Progress{}); !errors.Is(err, domain.ErrCancelled) {
				t.Errorf("Report = %v, want domain.ErrCancelled", err)
			}
		})
	}
}

// Cancelling a delete_bank is FAILED_PRECONDITION; on a deleting bank only
// delete_bank may be retried, any other retry is FAILED_PRECONDITION (ErrBankDeleting).
func TestControlDeleteBankAndDeletingBank(t *testing.T) {
	bg := context.Background()
	ops := newOps(t)
	oc := NewOperationControl(ops)
	del := ops.add(store.Job{Bank: bankC, Kind: domain.OperationKindDeleteBank, SerialKey: "bank"})
	retain := ops.add(store.Job{Bank: bankC, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1"})
	if _, err := oc.Cancel(bg, bankC, del); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("Cancel delete_bank = %v, want ErrFailedPrecondition", err)
	}
	if row := ops.row(del); row.status != domain.OperationStatusPending {
		t.Fatalf("delete_bank after the refused cancel: %s", row.status)
	}
	if _, err := oc.Cancel(bg, bankC, retain); err != nil {
		t.Fatalf("Cancel retain: %v", err)
	}
	ops.markDeleting(bankC)
	if js, _ := ops.Claim(bg, "w", []domain.OperationKind{domain.OperationKindDeleteBank}, 1); len(js) != 1 {
		t.Fatal("claim delete_bank")
	}
	if err := ops.Fail(bg, del, errors.New("crash"), nil); err != nil {
		t.Fatal(err)
	}
	_, err := oc.Retry(bg, bankC, retain)
	if !errors.Is(err, domain.ErrFailedPrecondition) || !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("Retry retain on a deleting bank = %v, want ErrBankDeleting (FAILED_PRECONDITION)", err)
	}
	if row := ops.row(retain); row.status != domain.OperationStatusCancelled {
		t.Fatalf("retain after the refused retry: %s", row.status)
	}
	op, err := oc.Retry(bg, bankC, del)
	if err != nil || op.Status != domain.OperationStatusPending {
		t.Fatalf("Retry delete_bank on a deleting bank = %+v, %v; want PENDING", op, err)
	}
}

func TestPendingChecker(t *testing.T) {
	bg := context.Background()
	clock := newClock()
	ops := newOps(t)
	pc := NewPendingChecker(ops, clock)
	due := consolidateJob(bankA)
	due.RunAt = clock.Now() // due-ness is judged by the checker's clock, not the database's insert time
	parked := consolidateJob(bankB)
	parked.RunAt = clock.Now().Add(time.Hour)
	ops.add(due)
	ops.add(parked)
	ops.add(store.Job{
		Bank: bankC, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Overrides: &domain.BankConfig{},
	}) // manual, with overrides: not joinable
	ops.markDeleting(domain.BankRef{Tenant: "t9", Bank: "gone"})
	rows := ops.count("true")
	tests := []struct {
		bank domain.BankRef
		want bool
	}{
		{bankA, true},  // due automatic consolidate
		{bankB, false}, // parked
		{bankC, false}, // not joinable
		{domain.BankRef{Tenant: "t9", Bank: "gone"}, false}, // tombstoned
	}
	for _, tt := range tests {
		if ok, err := pc.HasPending(bg, tt.bank, domain.OperationKindConsolidate); err != nil || ok != tt.want {
			t.Errorf("bank %v = %v, %v; want %v", tt.bank, ok, err, tt.want)
		}
	}
	if _, err := pc.HasPending(bg, domain.BankRef{Tenant: "t9", Bank: "x"}, domain.OperationKindConsolidate); !errors.Is(
		err, domain.ErrNotFound) {
		t.Errorf("unknown bank = %v, want ErrNotFound", err)
	}
	if n := ops.count("true"); n != rows {
		t.Errorf("probe left %d rows, want %d (always rolled back)", n, rows)
	}
	clock.Advance(2 * time.Hour)
	if ok, _ := pc.HasPending(bg, bankB, domain.OperationKindConsolidate); !ok {
		t.Error("a parked consolidate that became due must count")
	}
	broken := NewPendingChecker(&pruneStore{DB: ops, txErr: domain.ErrUnavailable}, clock)
	if _, err := broken.HasPending(bg, bankA, domain.OperationKindConsolidate); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("broken store = %v, want ErrUnavailable", err)
	}
	if _, err := pc.HasPending(bg, bankA, domain.OperationKindRetainDocument); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("other kind = %v, want ErrInvalidArgument", err)
	}
}
