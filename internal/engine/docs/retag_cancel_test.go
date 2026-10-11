package docs_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/store"
	"testing"
)

// cancellingStore cancels the caller's context while its transaction runs, so the commit is aborted.
type cancellingStore struct {
	store.DB
	cancel context.CancelFunc
}

// InTx runs fn after cancelling the context, simulating a cancel that lands mid-transaction.
func (s cancellingStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error {
		s.cancel()
		return fn(tx)
	})
}

// TestRetagApplyCancelledMidTx: a retag_document job whose context ends inside its
// transaction reports domain.ErrCancelled and commits nothing.
func TestRetagApplyCancelledMidTx(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := docs.NewRetagger(f.store).Retag(ctx, testBank, "d1", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	job := f.claimRetag(t)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := docs.NewRetagger(cancellingStore{DB: f.store, cancel: cancel}).Apply(cctx, job)
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Apply cancelled mid-tx: %v, want ErrCancelled", err)
	}
	us, err := f.store.Read().GetUnits(ctx, testBank, uids("o1"))
	if err != nil || len(us) != 1 {
		t.Errorf("o1 after a cancelled retag_document = %+v (%v), want it kept", us, err)
	}
	if cons := f.jobs(t, domain.OperationKindConsolidate); len(cons) != 0 {
		t.Errorf("a cancelled retag_document enqueued %d consolidate jobs, want none", len(cons))
	}
}
