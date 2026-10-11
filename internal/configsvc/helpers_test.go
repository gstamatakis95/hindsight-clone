package configsvc

import (
	"context"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
	"time"
)

// env is one test's store and the Service over it (no server config level).
type env struct {
	ctx   context.Context
	store *postgres.DB
	svc   *Service
}

func newEnv(t *testing.T) env {
	t.Helper()
	db := pgtest.NewStore(t)
	return env{
		ctx: context.Background(), store: db,
		svc: New(Deps{DB: db, Config: config.NewResolver(db, domain.BankConfig{})}),
	}
}

// tx runs fn in a transaction and fails the test on error.
func (e env) tx(t *testing.T, fn func(tx store.Tx) error) {
	t.Helper()
	if err := e.store.InTx(e.ctx, fn); err != nil {
		t.Fatalf("InTx: %v", err)
	}
}

// withBank stores base as bank b's config and dirs as its directives, and returns the Service.
func (e env) withBank(t *testing.T, b domain.BankRef, base domain.BankConfig, dirs ...domain.Directive) *Service {
	t.Helper()
	e.tx(t, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, b); err != nil {
			return err
		}
		if err := tx.Banks().SetConfig(e.ctx, b, base); err != nil {
			return err
		}
		for _, d := range dirs {
			d.Bank = b
			if _, err := tx.Directives().Create(e.ctx, d); err != nil {
				return err
			}
		}
		return nil
	})
	return e.svc
}

// tombstone marks bank b deleting.
func (e env) tombstone(t *testing.T, b domain.BankRef) {
	t.Helper()
	e.tx(t, func(tx store.Tx) error { return tx.Banks().MarkDeleting(e.ctx, b, time.Now()) })
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
