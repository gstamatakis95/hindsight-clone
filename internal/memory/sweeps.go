package memory

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// Settings of the stranded-fact sweep.
const (
	// strandedBatch caps the rederives one sweep enqueues.
	strandedBatch = 100
	// strandedRetryAfter is how long after a rederive ended the sweep may queue another one for the same
	// unit, so a fact whose rederive keeps failing is retried every few minutes, not every tick.
	strandedRetryAfter = 10 * time.Minute
)

// RederiveStranded re-enqueues rederive_memory for stranded facts (a NULL embedding left by a rederive that
// ended FAILED or CANCELLED at least 10 minutes ago), one short transaction per fact, with the payload and
// serial key curation uses (jobs.EnqueueRederive, no overrides). The scan skips units whose rederive is queued
// or running, so ticks don't pile jobs up; two workers ticking at once may queue a unit twice, which is
// harmless (rederive is idempotent). A bank being deleted or gone is skipped. It takes no tx.Seq: a job row is
// not memory content. It returns how many it queued.
func (s *Service) RederiveStranded(ctx context.Context) (int, error) {
	units, err := s.d.Maintenance.StrandedFacts(ctx, strandedRetryAfter, strandedBatch)
	if err != nil {
		return 0, fmt.Errorf("list stranded facts: %w", err)
	}
	n := 0
	var errs []error
	for _, u := range units {
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			_, _, err := jobs.EnqueueRederive(ctx, tx, u, nil)
			return err
		})
		switch {
		case errors.Is(err, domain.ErrBankDeleting), errors.Is(err, domain.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("unit %s: %w", u.ID, err))
		default:
			n++
		}
	}
	return n, errors.Join(errs...)
}

// SettleRetainBatches ends retain_batch parents whose children are all terminal (a parent is never run; its
// status aggregates its children) and returns how many it settled.
func (s *Service) SettleRetainBatches(ctx context.Context) (int, error) {
	n, err := s.d.Maintenance.SettleRetainBatches(ctx)
	if err != nil {
		return n, fmt.Errorf("settle retain batches: %w", err)
	}
	return n, nil
}
