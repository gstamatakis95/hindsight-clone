package worker

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"
)

func TestCheckpointReport(t *testing.T) {
	bg := context.Background()
	claimOne := func(t *testing.T, ops *fakeOps) port.OperationID {
		t.Helper()
		id := ops.add(retainJob("1"))
		if js, _ := ops.Claim(bg, "w1", []port.JobKind{domain.OperationKindRetainDocument}, 1); len(js) != 1 {
			t.Fatal("claim")
		}
		return id
	}
	t.Run("alive records progress", func(t *testing.T) {
		ops := newOps(newClock())
		id := claimOne(t, ops)
		if err := NewCheckpoint(ops, id).Report(bg, domain.Progress{Stage: "s", Processed: 1}); err != nil {
			t.Fatalf("Report: %v", err)
		}
		if p := ops.row(id).progress; len(p) != 1 || p[0].Processed != 1 {
			t.Errorf("progress = %+v", p)
		}
	})
	tests := []struct {
		name  string
		setup func(ops *fakeOps, id port.OperationID, cancel context.CancelFunc)
	}{
		{"cancel requested", func(ops *fakeOps, id port.OperationID, _ context.CancelFunc) {
			_, _ = ops.Cancel(bg, bankA, id)
		}},
		{"bank tombstoned", func(ops *fakeOps, _ port.OperationID, _ context.CancelFunc) { ops.deleting[bankA] = true }},
		{"row no longer processing", func(ops *fakeOps, id port.OperationID, _ context.CancelFunc) {
			_ = ops.Complete(bg, id, nil, nil)
		}},
		{"row gone", func(ops *fakeOps, id port.OperationID, _ context.CancelFunc) { delete(ops.rows, id) }},
		{"context done", func(_ *fakeOps, _ port.OperationID, cancel context.CancelFunc) { cancel() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := newOps(newClock())
			id := claimOne(t, ops)
			ctx, cancel := context.WithCancel(bg)
			defer cancel()
			tt.setup(ops, id, cancel)
			if err := NewCheckpoint(ops, id).Report(ctx, domain.Progress{}); !errors.Is(err, domain.ErrCancelled) {
				t.Errorf("Report = %v, want domain.ErrCancelled", err)
			}
		})
	}
}

func TestOperationControl(t *testing.T) {
	bg := context.Background()
	ops := newOps(newClock())
	oc := NewOperationControl(&fakeStore{ops: ops})
	id := ops.add(retainJob("1"))
	op, err := oc.Cancel(bg, bankA, id)
	if err != nil || op.Status != domain.OperationStatusCancelled {
		t.Fatalf("Cancel pending = %+v, %v; want CANCELLED", op, err)
	}
	if _, err := oc.Cancel(bg, bankA, id); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Errorf("Cancel terminal = %v, want ErrFailedPrecondition", err)
	}
	op, err = oc.Retry(bg, bankA, id)
	if err != nil || op.Status != domain.OperationStatusPending {
		t.Fatalf("Retry cancelled = %+v, %v; want PENDING", op, err)
	}
	if _, err := oc.Retry(bg, bankA, id); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Errorf("Retry pending = %v, want ErrFailedPrecondition", err)
	}
	if _, err := oc.Cancel(bg, bankB, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Cancel in another bank = %v, want ErrNotFound", err)
	}
}

// Accept (WP-20 item 2): cancelling a delete_bank is FAILED_PRECONDITION; on a deleting bank only
// delete_bank may be retried, any other retry is FAILED_PRECONDITION (ErrBankDeleting).
func TestControlDeleteBankAndDeletingBank(t *testing.T) {
	bg := context.Background()
	clock := newClock()
	ops := newOps(clock)
	oc := NewOperationControl(&fakeStore{ops: ops})
	del := ops.add(port.Job{Bank: bankC, Kind: domain.OperationKindDeleteBank, SerialKey: "bank"})
	retain := ops.add(port.Job{Bank: bankC, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1"})
	if _, err := oc.Cancel(bg, bankC, del); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("Cancel delete_bank = %v, want ErrFailedPrecondition", err)
	}
	if row := ops.row(del); row.status != domain.OperationStatusPending {
		t.Fatalf("delete_bank after the refused cancel: %s", row.status)
	}
	if _, err := oc.Cancel(bg, bankC, retain); err != nil {
		t.Fatalf("Cancel retain: %v", err)
	}
	ops.deleting[bankC] = true
	if js, _ := ops.Claim(bg, "w", []port.JobKind{domain.OperationKindDeleteBank}, 1); len(js) != 1 {
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
	ops := newOps(clock)
	pc := NewPendingChecker(&fakeStore{ops: ops}, clock)
	parked := consolidateJob(bankB)
	parked.RunAt = clock.Now().Add(time.Hour)
	ops.add(consolidateJob(bankA))
	ops.add(parked)
	ops.add(port.Job{
		Bank: bankC, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Overrides: &domain.BankConfig{},
	}) // manual, with overrides: not joinable
	ops.deleting[port.BankRef{Tenant: "t9", Bank: "gone"}] = true
	rows := ops.count(func(*fakeRow) bool { return true })
	tests := []struct {
		bank port.BankRef
		want bool
	}{
		{bankA, true},  // due automatic consolidate
		{bankB, false}, // parked
		{bankC, false}, // not joinable
		{port.BankRef{Tenant: "t9", Bank: "x"}, false},
		{port.BankRef{Tenant: "t9", Bank: "gone"}, false}, // tombstoned
	}
	for _, tt := range tests {
		if ok, err := pc.HasPending(bg, tt.bank, domain.OperationKindConsolidate); err != nil || ok != tt.want {
			t.Errorf("bank %v = %v, %v; want %v", tt.bank, ok, err, tt.want)
		}
	}
	if n := ops.count(func(*fakeRow) bool { return true }); n != rows {
		t.Errorf("probe left %d rows, want %d (always rolled back)", n, rows)
	}
	clock.Advance(2 * time.Hour)
	if ok, _ := pc.HasPending(bg, bankB, domain.OperationKindConsolidate); !ok {
		t.Error("a parked consolidate that became due must count")
	}
	broken := NewPendingChecker(&fakeStore{ops: ops, txErr: domain.ErrUnavailable}, clock)
	if _, err := broken.HasPending(bg, bankA, domain.OperationKindConsolidate); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("broken store = %v, want ErrUnavailable", err)
	}
	if _, err := pc.HasPending(bg, bankA, domain.OperationKindRetainDocument); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("other kind = %v, want ErrInvalidArgument", err)
	}
}
