package worker

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
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

func TestPendingChecker(t *testing.T) {
	bg := context.Background()
	ops := newOps(newClock())
	pc := NewPendingChecker(&fakeStore{ops: ops})
	ops.add(consolidateJob(bankA))
	if ok, err := pc.HasPending(bg, bankA, domain.OperationKindConsolidate); err != nil || !ok {
		t.Errorf("bank A = %v, %v; want pending", ok, err)
	}
	if ok, err := pc.HasPending(bg, bankB, domain.OperationKindConsolidate); err != nil || ok {
		t.Errorf("bank B = %v, %v; want none", ok, err)
	}
	broken := NewPendingChecker(&fakeStore{ops: ops, txErr: domain.ErrUnavailable})
	if _, err := broken.HasPending(bg, bankA, domain.OperationKindConsolidate); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("broken store = %v, want ErrUnavailable", err)
	}
}
