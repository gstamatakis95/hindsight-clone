package observation

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
	"time"
)

// waitingBanks is a store.Maintenance whose UnconsolidatedBanks returns fixed banks with their oldest waiting
// fact.
type waitingBanks struct {
	store.Maintenance
	banks []store.UnconsolidatedBank
}

// UnconsolidatedBanks returns the fixed banks.
func (w waitingBanks) UnconsolidatedBanks(context.Context, time.Time) ([]store.UnconsolidatedBank, error) {
	return w.banks, nil
}

// reconcileEnv creates each bank with its config and returns a Service whose sweep sees banks waiting since
// oldest.
func reconcileEnv(t *testing.T, oldest time.Time, cfgs map[domain.BankRef]domain.BankConfig,
	extra ...domain.BankRef,
) (*env, *Service) {
	t.Helper()
	e := newEnv(t, domain.BankConfig{})
	var w waitingBanks
	e.tx(func(tx store.Tx) error {
		for b, cfg := range cfgs {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
			if err := tx.Banks().SetConfig(t.Context(), b, cfg); err != nil {
				return err
			}
		}
		return nil
	})
	for b := range cfgs {
		w.banks = append(w.banks, store.UnconsolidatedBank{Bank: b, Oldest: oldest})
	}
	for _, b := range extra {
		w.banks = append(w.banks, store.UnconsolidatedBank{Bank: b, Oldest: oldest})
	}
	e.deps.Maintenance = w
	return e, e.service()
}

// pendingConsolidates counts b's consolidate operations.
func (e *env) pendingConsolidates(b domain.BankRef) int {
	e.t.Helper()
	var ops []domain.Operation
	e.tx(func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.t.Context(), b, store.OperationFilter{
			Kind: domain.OperationKindConsolidate, PageSize: 100,
		})
		return err
	})
	return len(ops)
}

// observationsConfig is a config with only the observations group set.
func observationsConfig(o domain.ObservationConfig) domain.BankConfig {
	return domain.BankConfig{Observations: &o}
}

// TestReconcileSkipsDisabledBanks: the sweep consolidates only banks whose effective config leaves automatic
// consolidation and observations on; a bank that doesn't exist is skipped silently, and a second sweep joins
// the queued job.
func TestReconcileSkipsDisabledBanks(t *testing.T) {
	a, b, c := domain.NewBankRef("t", "org", "a"), domain.NewBankRef("t", "org", "b"),
		domain.NewBankRef("t", "org", "c")
	gone := domain.NewBankRef("t", "org", "gone")
	e, s := reconcileEnv(t, time.Time{}, map[domain.BankRef]domain.BankConfig{
		a: observationsConfig(domain.ObservationConfig{AutoConsolidation: ptr(true), Enabled: ptr(true)}),
		b: observationsConfig(domain.ObservationConfig{AutoConsolidation: ptr(false)}),
		c: observationsConfig(domain.ObservationConfig{Enabled: ptr(false)}),
	}, gone)
	if n, err := s.ReconcileConsolidation(t.Context()); err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v; want 1 inserted", n, err)
	}
	if n, err := s.ReconcileConsolidation(t.Context()); err != nil || n != 0 {
		t.Fatalf("second reconcile = %d, %v; want 0 (joined)", n, err)
	}
	for bk, want := range map[domain.BankRef]int{a: 1, b: 0, c: 0} {
		if got := e.pendingConsolidates(bk); got != want {
			t.Errorf("bank %v: %d consolidate jobs, want %d", bk, got, want)
		}
	}
}

// TestReconcileGrace: the sweep consolidates a bank only once its oldest waiting fact is older than the bank's
// own observations.reconcile_grace_seconds (default 10 min), so a skip_consolidation retain followed by a
// manual Consolidate within the grace is not pre-empted.
func TestReconcileGrace(t *testing.T) {
	a, b, c := domain.NewBankRef("t", "org", "a"), domain.NewBankRef("t", "org", "b"),
		domain.NewBankRef("t", "org", "c")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e, s := reconcileEnv(t, now.Add(-5*time.Minute), map[domain.BankRef]domain.BankConfig{
		a: observationsConfig(domain.ObservationConfig{ReconcileGraceSeconds: ptr(180)}),
		b: {},
		c: observationsConfig(domain.ObservationConfig{ReconcileGraceSeconds: ptr(600)}),
	})
	e.clock.Set(now)
	if _, err := s.ReconcileConsolidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	for bk, want := range map[domain.BankRef]int{a: 1, b: 0, c: 0} {
		if got := e.pendingConsolidates(bk); got != want {
			t.Errorf("bank %v: %d consolidate jobs, want %d", bk, got, want)
		}
	}
}
