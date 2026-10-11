package maintenance

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
)

// strandedBatch caps the rederives one tick enqueues.
const strandedBatch = 100

// rederiveStranded re-enqueues rederive_memory for stranded facts, one short transaction per fact, with the
// payload and serial key curation uses (jobs.EnqueueRederive, no overrides). The source skips units whose
// rederive is queued or running, so ticks don't pile jobs up; two workers ticking at once may queue a unit
// twice, which is harmless (rederive is idempotent). It returns how many it queued.
func (s *Service) rederiveStranded(ctx context.Context) (int, error) {
	if s.d.Stranded == nil {
		return 0, nil
	}
	units, err := s.d.Stranded.StrandedFacts(ctx, s.d.Config.StrandedRetryAfter, strandedBatch)
	if err != nil {
		return 0, fmt.Errorf("list stranded facts: %w", err)
	}
	n := 0
	var errs []error
	for _, u := range units {
		err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
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

// settleBatches ends retain_batch parents whose children are all terminal.
func (s *Service) settleBatches(ctx context.Context) (int, error) {
	if s.d.Batches == nil {
		return 0, nil
	}
	n, err := s.d.Batches.SettleRetainBatches(ctx)
	if err != nil {
		return n, fmt.Errorf("settle retain batches: %w", err)
	}
	return n, nil
}
