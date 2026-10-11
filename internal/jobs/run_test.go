package jobs

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
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

func TestRunReport(t *testing.T) {
	t.Run("alive records progress", func(t *testing.T) {
		ops := newOps(t)
		id := claimOne(t, ops)
		if err := NewRun(ops, store.Job{ID: id}).Report(t.Context(), domain.Progress{Stage: "s", Processed: 1}); err != nil {
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
			if err := NewRun(ops, store.Job{ID: id}).Report(ctx, domain.Progress{}); !errors.Is(err, domain.ErrCancelled) {
				t.Errorf("Report = %v, want domain.ErrCancelled", err)
			}
		})
	}
}
