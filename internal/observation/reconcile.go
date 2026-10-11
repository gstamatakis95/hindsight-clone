package observation

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// defaultReconcileGrace is how old an unconsolidated unit must be before the sweep consolidates its bank when
// its config sets no observations.reconcile_grace_seconds, so skip_consolidation followed by a manual
// Consolidate is not pre-empted.
const defaultReconcileGrace = 10 * time.Minute

// ReconcileConsolidation is the maintenance sweep that enqueues an automatic consolidate for every bank with a unit
// left unconsolidated for longer than the bank's reconcile grace, unless the bank's effective config turns automatic
// consolidation or observations off (docs/design/consolidation.md#enqueue-sources). One short transaction per bank.
// jobs.EnqueueAutoConsolidate joins the bank's queued or running automatic consolidate (a running one re-enqueues
// itself while units remain), so concurrent sweeps insert at most one, and a sweep never queues a job that would make a
// failing running consolidate skip its retry. It takes no change seq: nothing it writes is memory content. A bank being
// deleted (or already gone) is skipped; other errors don't stop the remaining banks. It returns how many jobs it
// inserted.
func (s *Service) ReconcileConsolidation(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	banks, err := s.d.Maintenance.UnconsolidatedBanks(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list banks to consolidate: %w", err)
	}
	var keep []domain.BankRef
	var errs []error
	for _, b := range banks {
		ok, err := s.autoConsolidates(ctx, b, now)
		switch {
		case gone(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: config: %w", b.Bank.Tenant, b.Bank.Bank, err))
		case ok:
			keep = append(keep, b.Bank)
		}
	}
	n, err := s.enqueueAuto(ctx, keep)
	return n, errors.Join(append(errs, err)...)
}

// autoConsolidates reports whether the sweep consolidates b now: its effective observations.auto_consolidation
// and observations.enabled are both unset or true, and its oldest waiting fact is older than its
// observations.reconcile_grace_seconds (unset = defaultReconcileGrace).
func (s *Service) autoConsolidates(ctx context.Context, b store.UnconsolidatedBank, now time.Time) (bool, error) {
	cfg, err := s.d.Config.Effective(ctx, b.Bank)
	if err != nil {
		return false, fmt.Errorf("effective config: %w", err)
	}
	obs := cfg.GetObservations()
	on := func(p *bool) bool { return p == nil || *p }
	if !on(obs.AutoConsolidation) || !on(obs.Enabled) {
		return false, nil
	}
	grace := defaultReconcileGrace
	if g := obs.ReconcileGraceSeconds; g != nil {
		grace = time.Duration(*g) * time.Second
	}
	return b.Oldest.Before(now.Add(-grace)), nil
}

// enqueueAuto enqueues (or joins) each bank's automatic consolidate with jobs.EnqueueAutoConsolidate, one
// short transaction each, and returns how many it inserted. A bank being deleted (or gone) is skipped; other
// errors don't stop the rest.
func (s *Service) enqueueAuto(ctx context.Context, banks []domain.BankRef) (int, error) {
	inserted := 0
	var errs []error
	for _, b := range banks {
		var joined bool
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			var err error
			_, joined, err = jobs.EnqueueAutoConsolidate(ctx, tx, b)
			return err
		})
		switch {
		case gone(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: %w", b.Tenant, b.Bank, err))
		case !joined:
			inserted++
		}
	}
	return inserted, errors.Join(errs...)
}

// gone reports an error meaning the bank is being deleted or no longer exists.
func gone(err error) bool {
	return errors.Is(err, domain.ErrBankDeleting) || errors.Is(err, domain.ErrNotFound)
}
