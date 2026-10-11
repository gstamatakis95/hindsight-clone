package bank

import (
	"context"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
	"time"
)

// env is one test's store, fake clock and fake blob store.
type env struct {
	ctx   context.Context
	clock *clock.Manual
	store *postgres.DB
	blob  *fake.Blob
}

func newEnv(t *testing.T) env {
	t.Helper()
	clock := clock.NewManual(time.Time{})
	return env{ctx: context.Background(), clock: clock, store: pgtest.NewStore(t), blob: fake.NewBlob()}
}

// svc returns the Service over e's store, blob store and clock, with the delete bounds cfg.
func (e env) svc(cfg DeleteConfig) *Service {
	return e.svcOver(e.store, e.blob, cfg)
}

// svcOver returns the Service over db and bs (test doubles wrapping e's), with the delete bounds cfg.
func (e env) svcOver(db store.DB, bs blob.Store, cfg DeleteConfig) *Service {
	return New(Deps{DB: db, Maintenance: e.store, Blob: bs, Clock: e.clock, Delete: cfg})
}

// tx runs fn in a transaction and fails the test on error.
func (e env) tx(t *testing.T, fn func(tx store.Tx) error) {
	t.Helper()
	if err := e.store.InTx(e.ctx, fn); err != nil {
		t.Fatalf("InTx: %v", err)
	}
}

// ensure creates bank b.
func (e env) ensure(t *testing.T, b domain.BankRef) {
	t.Helper()
	e.tx(t, func(tx store.Tx) error {
		_, err := tx.Banks().Ensure(e.ctx, b)
		return err
	})
}

// getOp reads one operation.
func (e env) getOp(t *testing.T, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	t.Helper()
	var (
		op domain.Operation
		db store.DB = e.store
	)
	err := db.InTx(e.ctx, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(e.ctx, b, id)
		return err
	})
	return op, err
}

// directives lists bank b's stored directives.
func (e env) directives(t *testing.T, b domain.BankRef) []domain.Directive {
	t.Helper()
	var out []domain.Directive
	e.tx(t, func(tx store.Tx) error {
		var err error
		out, err = tx.Directives().List(e.ctx, b, false)
		return err
	})
	return out
}

// names returns the directive names, sorted.
func names(ds []domain.Directive) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name)
	}
	slices.Sort(out)
	return out
}
