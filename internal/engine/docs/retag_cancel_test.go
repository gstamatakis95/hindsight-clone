package docs_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/port"
	"testing"
)

// cancellingStore cancels the caller's context while its transaction runs, so the fake aborts the commit.
type cancellingStore struct {
	port.Store
	cancel context.CancelFunc
}

// InTx runs fn after cancelling the context, simulating a cancel that lands mid-transaction.
func (s cancellingStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		s.cancel()
		return fn(tx)
	})
}

// TestRetagApplyCancelledMidTx (review round 2, LOW): a retag_document job whose context ends inside its
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
	err := docs.NewRetagger(cancellingStore{Store: f.store, cancel: cancel}).Apply(cctx, job)
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Apply cancelled mid-tx: %v, want ErrCancelled", err)
	}
	us, err := f.store.Read().GetUnits(ctx, testBank, []port.UnitID{"f1"})
	if err != nil || len(us) != 1 || len(us[0].Tags) != 2 {
		t.Errorf("f1 after a cancelled retag = %+v (%v), want its old tags", us, err)
	}
}
