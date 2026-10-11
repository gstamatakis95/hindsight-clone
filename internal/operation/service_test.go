package operation

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"
)

// inTx runs fn in a transaction on db and fails the test on error.
func inTx(t *testing.T, db store.DB, fn func(tx store.Tx) error) {
	t.Helper()
	if err := db.InTx(t.Context(), fn); err != nil {
		t.Fatal(err)
	}
}

// TestListEventsPageBounds checks the paging convention (domain.ListPage) on ListEvents: page_size 0 returns the
// default (100), not the whole history; a size above the cap returns 1000; a small size is kept; a negative size
// is ErrInvalidArgument naming page_size.
func TestListEventsPageBounds(t *testing.T) {
	db := pgtest.NewStore(t)
	b := domain.BankRef{Tenant: "t", Bank: "events"}
	total := domain.ListPage.Max + 50
	for range total { // one seq per event, so the soft limit never extends a page over a seq group
		inTx(t, db, func(tx store.Tx) error {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
			seq, err := tx.Seq(t.Context(), b)
			if err != nil {
				return err
			}
			return tx.Events().Append(t.Context(), domain.LifecycleEvent{
				Bank: b, Seq: seq, Type: domain.EventTypeRetainCompleted,
			})
		})
	}
	s := New(Deps{DB: db})
	for _, tc := range []struct{ size, want int }{
		{0, domain.ListPage.Default}, {3, 3}, {total, domain.ListPage.Max},
	} {
		events, _, err := s.ListEvents(t.Context(), b, 0, tc.size, nil)
		if err != nil {
			t.Fatalf("ListEvents(page_size %d): %v", tc.size, err)
		}
		if len(events) != tc.want {
			t.Errorf("ListEvents(page_size %d) returned %d events, want %d", tc.size, len(events), tc.want)
		}
	}
	_, _, err := s.ListEvents(t.Context(), b, 0, -1, nil)
	if vs := domain.FieldViolations(err); !errors.Is(err, domain.ErrInvalidArgument) || len(vs) != 1 ||
		vs[0].Field != "page_size" {
		t.Errorf("ListEvents(page_size -1) = %v (%v), want ErrInvalidArgument on page_size", err, vs)
	}
}

// enqueue adds j in its own transaction and returns its id.
func enqueue(t *testing.T, db store.DB, j store.Job) domain.OperationID {
	t.Helper()
	j.Payload = json.RawMessage(`{}`)
	var id domain.OperationID
	inTx(t, db, func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), j)
		return err
	})
	return id
}

// status reads one operation's status.
func status(t *testing.T, db store.DB, b domain.BankRef, id domain.OperationID) domain.OperationStatus {
	t.Helper()
	var op domain.Operation
	inTx(t, db, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(t.Context(), b, id)
		return err
	})
	return op.Status
}

// TestCancelAndRetryRules: cancelling a delete_bank is FAILED_PRECONDITION; on a deleting bank only delete_bank may
// be retried, any other retry is FAILED_PRECONDITION (ErrBankDeleting); GetOperation of a deleting bank answers
// only for its delete_bank operation, which is NOT_FOUND once the bank is gone.
func TestCancelAndRetryRules(t *testing.T) {
	db := pgtest.NewStore(t)
	ctx := t.Context()
	b := domain.BankRef{Tenant: "t", Bank: "c"}
	inTx(t, db, func(tx store.Tx) error { _, err := tx.Banks().Ensure(ctx, b); return err })
	s := New(Deps{DB: db})
	del := enqueue(t, db, store.Job{Bank: b, Kind: domain.OperationKindDeleteBank, SerialKey: "bank"})
	retain := enqueue(t, db, store.Job{Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1"})
	if _, err := s.CancelOperation(ctx, b, del); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("Cancel delete_bank = %v, want ErrFailedPrecondition", err)
	}
	if st := status(t, db, b, del); st != domain.OperationStatusPending {
		t.Fatalf("delete_bank after the refused cancel: %s", st)
	}
	if _, err := s.CancelOperation(ctx, b, retain); err != nil {
		t.Fatalf("Cancel retain: %v", err)
	}
	inTx(t, db, func(tx store.Tx) error { return tx.Banks().MarkDeleting(ctx, b, time.Now()) })
	failDelete(t, db, del)
	_, err := s.RetryOperation(ctx, b, retain)
	if !errors.Is(err, domain.ErrFailedPrecondition) || !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("Retry retain on a deleting bank = %v, want ErrBankDeleting (FAILED_PRECONDITION)", err)
	}
	if st := status(t, db, b, retain); st != domain.OperationStatusCancelled {
		t.Fatalf("retain after the refused retry: %s", st)
	}
	op, err := s.RetryOperation(ctx, b, del)
	if err != nil || op.Status != domain.OperationStatusPending {
		t.Fatalf("Retry delete_bank on a deleting bank = %+v, %v; want PENDING", op, err)
	}
	if got, err := s.GetOperation(ctx, b, del); err != nil || got.ID != del {
		t.Errorf("GetOperation(delete_bank) on a deleting bank = %+v, %v; want the operation", got, err)
	}
	if _, err := s.GetOperation(ctx, b, retain); !errors.Is(err, domain.ErrBankNotFound) {
		t.Errorf("GetOperation(retain) on a deleting bank = %v, want ErrBankNotFound", err)
	}
	inTx(t, db, func(tx store.Tx) error { return tx.Banks().Delete(ctx, b) })
	if _, err := s.GetOperation(ctx, b, del); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOperation(delete_bank) once the bank is gone = %v, want ErrNotFound", err)
	}
}

// failDelete claims the delete_bank job id and fails it.
func failDelete(t *testing.T, db *postgres.DB, id domain.OperationID) {
	t.Helper()
	js, err := db.Claim(t.Context(), "w", []domain.OperationKind{domain.OperationKindDeleteBank}, 1)
	if err != nil || len(js) != 1 || js[0].ID != id {
		t.Fatalf("claim delete_bank: %v %v", js, err)
	}
	if err := db.Fail(t.Context(), id, errors.New("crash"), nil); err != nil {
		t.Fatal(err)
	}
}
