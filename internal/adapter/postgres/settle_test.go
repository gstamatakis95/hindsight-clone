package postgres_test

import (
	"encoding/json"
	"errors"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// settleBatch enqueues a retain_batch parent with n retain_document children and returns their ids.
func settleBatch(t *testing.T, s *postgres.Store, n int) (port.OperationID, []port.OperationID) {
	t.Helper()
	var parent port.OperationID
	kids := make([]port.OperationID, n)
	if err := fuTx(t, s, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), fuBank); err != nil {
			return err
		}
		var err error
		parent, _, err = tx.Queue().Enqueue(t.Context(), port.Job{
			Bank: fuBank, Kind: domain.OperationKindRetainBatch, Payload: json.RawMessage(`{}`),
		})
		if err != nil {
			return err
		}
		for i := range kids {
			kids[i], _, err = tx.Queue().Enqueue(t.Context(), port.Job{
				Bank: fuBank, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + string(rune('a'+i)),
				Payload: json.RawMessage(`{}`), ParentID: parent,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return parent, kids
}

// settleStatus reads an operation's status.
func settleStatus(t *testing.T, s *postgres.Store, id port.OperationID) domain.Operation {
	t.Helper()
	var op domain.Operation
	if err := fuTx(t, s, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(t.Context(), fuBank, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return op
}

// TestSettleRetainBatches checks a retain_batch parent stays PENDING while a child runs, then aggregates its
// children: COMPLETED when all completed, FAILED when one failed.
func TestSettleRetainBatches(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	parent, kids := settleBatch(t, s, 2)
	jobs, err := s.Claim(ctx, "w", []port.JobKind{domain.OperationKindRetainDocument}, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	if err := s.Complete(ctx, kids[0], nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 0 {
		t.Fatalf("settled %d with a child running: %v", n, err)
	}
	if err := s.Complete(ctx, kids[1], nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 1 {
		t.Fatalf("settled %d: %v", n, err)
	}
	if op := settleStatus(t, s, parent); op.Status != domain.OperationStatusCompleted {
		t.Fatalf("parent %s", op.Status)
	}
	if n, _ := s.SettleRetainBatches(ctx); n != 0 {
		t.Fatalf("settled again: %d", n)
	}
}

// TestSettleRetainBatchFailedChild checks a parent with a failed child ends FAILED with an error.
func TestSettleRetainBatchFailedChild(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	failedParent, kids2 := settleBatch(t, s, 1)
	if _, err := s.Claim(ctx, "w", []port.JobKind{domain.OperationKindRetainDocument}, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, kids2[0], errors.New("boom"), nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 1 {
		t.Fatalf("settled %d: %v", n, err)
	}
	if op := settleStatus(t, s, failedParent); op.Status != domain.OperationStatusFailed || op.Error == "" {
		t.Fatalf("parent with a failed child: %s %q", op.Status, op.Error)
	}
}
