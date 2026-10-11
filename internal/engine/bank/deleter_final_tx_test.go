package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"sync/atomic"
	"testing"
)

// hidingStore hides PROCESSING operations from Queue().List while hide is set, so a test can make the
// deleter's first busy check pass and only its re-check inside the Banks().Delete transaction see the job.
type hidingStore struct {
	port.Store
	hide *atomic.Bool
}

// InTx runs fn over a Tx whose queue hides processing rows while hide is set.
func (s hidingStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error { return fn(hidingTx{Tx: tx, hide: s.hide}) })
}

// hidingTx is a Tx whose Queue hides processing rows while hide is set.
type hidingTx struct {
	port.Tx
	hide *atomic.Bool
}

// Queue returns the hiding queue.
func (t hidingTx) Queue() port.Queue { return hidingQueue{Queue: t.Tx.Queue(), hide: t.hide} }

// hidingQueue drops processing rows from List while hide is set.
type hidingQueue struct {
	port.Queue
	hide *atomic.Bool
}

// List lists operations, returning none for a processing-status filter while hide is set.
func (q hidingQueue) List(
	ctx context.Context, b port.BankRef, f port.OperationFilter,
) ([]domain.Operation, string, error) {
	if q.hide.Load() && f.Status == domain.OperationStatusProcessing {
		return nil, "", nil
	}
	return q.Queue.List(ctx, b, f) //nolint:wrapcheck // test double passes the port's error through
}

// unhideBlob is the blob store whose DeletePrefix makes the hidden processing job visible afterwards.
type unhideBlob struct {
	port.Blob
	hide *atomic.Bool
}

// DeletePrefix deletes the prefix, then stops hiding processing jobs.
func (u unhideBlob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	n, err := u.Blob.DeletePrefix(ctx, prefix)
	u.hide.Store(false)
	return n, err //nolint:wrapcheck // test double passes the port's error through
}

// TestDeleteFinalTxRechecksProcessingJobs: the busy re-check inside the transaction
// that deletes the bank row catches a processing job the pre-blob check missed; the bank row stays
// tombstoned, a delayed continuation is queued and the run reports done=false.
func TestDeleteFinalTxRechecksProcessingJobs(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	_, claimed := seedBank(t, e, b)
	hide := &atomic.Bool{}
	hide.Store(true)
	store := hidingStore{Store: e.store, hide: hide}
	d := NewBankDeleter(store, unhideBlob{Blob: e.blob, hide: hide}, e.store, e.clock, DeleterConfig{})
	if _, err := d.Tombstone(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	done, err := d.Run(e.ctx, j)
	if err != nil || done {
		t.Fatalf("run: done=%v err=%v, want blocked by the in-tx re-check", done, err)
	}
	if hide.Load() {
		t.Fatal("the run never reached the blob delete (the pre-blob check should have been fooled)")
	}
	var bank domain.Bank
	e.tx(t, func(tx port.Tx) error { var err error; bank, err = tx.Banks().Get(e.ctx, b); return err })
	if !bank.Deleting() {
		t.Errorf("bank row after a blocked final tx = %+v, want still tombstoned", bank)
	}
	if delayedContinuation(t, e, j) == nil {
		t.Errorf("no delayed continuation while job %s is processing", claimed)
	}
}
