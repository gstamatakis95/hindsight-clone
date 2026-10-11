package bank

import (
	"context"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"strconv"
	"strings"
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
		s  store.DB = e.store
	)
	err := s.InTx(e.ctx, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(e.ctx, b, id)
		return err
	})
	return op, err
}

// previewer stores base as bank b's config and dirs as its directives, and returns a PromptPreviewer over a real
// config resolver (no server level).
func (e env) previewer(
	t *testing.T, b domain.BankRef, base domain.BankConfig, dirs ...domain.Directive,
) PromptPreviewer {
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
			if _, err := tx.Directives().Upsert(e.ctx, d); err != nil {
				return err
			}
		}
		return nil
	})
	return NewPromptPreviewer(config.NewResolver(e.store, domain.BankConfig{}), e.store)
}

// statsStub serves BankStats and TagCounts from fixed data.
type statsStub struct {
	stats domain.BankStats
	tags  []domain.TagCount // sorted by tag
	calls []string          // recorded "source|prefix|after|limit"
}

func (s *statsStub) BankStats(context.Context, domain.BankRef) (domain.BankStats, error) {
	return s.stats, nil
}

func (s *statsStub) TagCounts(
	_ context.Context, _ domain.BankRef, source domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	s.calls = append(s.calls, strings.Join([]string{string(source), prefix, after, itoa(limit)}, "|"))
	var out []domain.TagCount
	for _, tc := range s.tags {
		if strings.HasPrefix(tc.Tag, prefix) && tc.Tag > after && len(out) < limit {
			out = append(out, tc)
		}
	}
	return out, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// names returns the directive names, sorted.
func names(ds []domain.Directive) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name)
	}
	slices.Sort(out)
	return out
}
