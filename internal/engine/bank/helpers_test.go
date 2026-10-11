package bank

import (
	"context"
	"encoding/json"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// env is one test's fake store, clock and blob store.
type env struct {
	ctx   context.Context
	clock *fake.Clock
	store *fake.Store
	blob  *fake.Blob
}

func newEnv(t *testing.T) env {
	t.Helper()
	clock := fake.NewClock(time.Time{})
	return env{ctx: context.Background(), clock: clock, store: fake.NewStore(clock), blob: fake.NewBlob()}
}

// tx runs fn in a transaction and fails the test on error.
func (e env) tx(t *testing.T, fn func(tx port.Tx) error) {
	t.Helper()
	if err := e.store.InTx(e.ctx, fn); err != nil {
		t.Fatalf("InTx: %v", err)
	}
}

// ensure creates bank b.
func (e env) ensure(t *testing.T, b port.BankRef) {
	t.Helper()
	e.tx(t, func(tx port.Tx) error {
		_, err := tx.Banks().Ensure(e.ctx, b)
		return err
	})
}

// getOp reads one operation.
func (e env) getOp(t *testing.T, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	t.Helper()
	var (
		op domain.Operation
		s  port.Store = e.store
	)
	err := s.InTx(e.ctx, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(e.ctx, b, id)
		return err
	})
	return op, err
}

// refreshStub enqueues a refresh_mental_model job like WP-12 would, inside the caller's tx.
type refreshStub struct{}

func (refreshStub) QueueRefresh(
	ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID,
) (port.OperationID, error) {
	payload, _ := json.Marshal(map[string]string{"model_id": string(id)})
	op, _, err := tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:" + string(id),
		Payload: payload, Dedupe: port.DedupePending,
	})
	return op, err
}

// cfgStub resolves the effective config as base < overrides.
type cfgStub struct{ base domain.BankConfig }

func (c cfgStub) Effective(_ context.Context, _ port.BankRef, o ...domain.BankConfig) (domain.BankConfig, error) {
	return config.Merge(append([]domain.BankConfig{c.base}, o...)...), nil
}

func (c cfgStub) ForScope(_ context.Context, _ port.ScopeKey, r *domain.BankConfig) (domain.BankConfig, error) {
	if r == nil {
		return c.base, nil
	}
	return config.Merge(c.base, *r), nil
}

// dirStub returns fixed directives.
type dirStub []domain.Directive

func (d dirStub) Select(context.Context, port.BankRef, []string, bool) ([]domain.Directive, error) {
	return d, nil
}

// statsStub serves BankStats and TagCounts from fixed data.
type statsStub struct {
	stats domain.BankStats
	tags  []domain.TagCount // sorted by tag
	calls []string          // recorded "source|prefix|after|limit"
}

func (s *statsStub) BankStats(context.Context, port.BankRef) (domain.BankStats, error) {
	return s.stats, nil
}

func (s *statsStub) TagCounts(
	_ context.Context, _ port.BankRef, source domain.TagSource, prefix, after string, limit int,
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
