package maintenance

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// Batch sizes of one graph_maintenance transaction.
const (
	// DirtyBatch is how many graph_dirty units one batch tops up.
	DirtyBatch = 100
	// EntityBatch is how many entity_maintenance entries one batch examines.
	EntityBatch = 500
)

// RunGraphJob is the graph_maintenance job: it drains the bank's graph_dirty and entity_maintenance queues
// within GraphBudget and re-enqueues itself (jobs.Requeue, DedupePending) when work is left. When the drain
// reports done it re-checks both queues as its last step: a removal that committed after the drain's final
// empty batch enqueued graph_maintenance with DedupePendingOrRunning and so joined this running row, and
// without the re-check its rows would wait for the next removal. Every batch is idempotent,
// so a run that repeats after a reclaim only redoes uncleared rows. A tombstoned or deleted bank stops the
// run with domain.ErrCancelled.
func (s *Service) RunGraphJob(ctx context.Context, run *jobs.Run) error {
	j := run.Job
	done, err := s.drainGraph(ctx, j.Bank, s.d.Config.GraphBudget)
	if err != nil {
		return fmt.Errorf("graph_maintenance: %w", err)
	}
	return s.continueGraph(ctx, j, done)
}

// continueGraph re-enqueues the graph job j when its drain ran out of budget (done=false), or when it
// finished but the re-check finds queued rows.
func (s *Service) continueGraph(ctx context.Context, j store.Job, done bool) error {
	if done {
		left, err := s.graphWorkLeft(ctx, j.Bank)
		if err != nil {
			return err
		}
		if !left {
			return nil
		}
	}
	return jobs.Requeue(ctx, s.d.Store, j, jobs.SerialKeyGraph, time.Time{})
}

// graphWorkLeft reports whether b has graph_dirty or entity_maintenance rows queued; a bank being deleted or
// gone has none.
func (s *Service) graphWorkLeft(ctx context.Context, b domain.BankRef) (bool, error) {
	left := false
	err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
		dirty, err := tx.Units().DirtyLinks(ctx, b, 1)
		if err != nil {
			return err
		}
		pending, err := tx.Entities().PendingMaintenance(ctx, b, 1)
		if err != nil {
			return err
		}
		left = len(dirty) > 0 || len(pending) > 0
		return nil
	})
	if gone(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph re-check: %w", err)
	}
	return left, nil
}

// drainGraph commits one batch per transaction (up to DirtyBatch link top-ups plus up to EntityBatch entity
// prunes) until a batch finds both queues empty (done=true) or the budget has run out (done=false). done is
// only ever reported by a batch that found both queues empty after the last batch that did work, so work
// queued while a batch ran (even inside it) is drained before the run ends. The
// budget is checked between batches only, so a batch is never cut in half. Neither step takes the bank's
// change seq: links and entity rows are not memory content, so models must not go stale over them.
func (s *Service) drainGraph(ctx context.Context, b domain.BankRef, budget time.Duration) (bool, error) {
	deadline := s.d.Clock.Now().Add(budget)
	for {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: graph maintenance of %s: %w", domain.ErrCancelled, b, err)
		}
		var linked, pruned int
		err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
			linked, pruned = 0, 0
			if err := checkBank(ctx, tx, b); err != nil {
				return err
			}
			var err error
			if linked, err = s.topUpLinks(ctx, tx, b, DirtyBatch); err != nil {
				return err
			}
			pruned, err = pruneEntities(ctx, tx, b, EntityBatch)
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return false, fmt.Errorf("%w: graph maintenance of %s: %w", domain.ErrCancelled, b, err)
			}
			return false, err
		}
		if linked == 0 && pruned == 0 {
			return true, nil
		}
		if !s.d.Clock.Now().Before(deadline) {
			return false, nil
		}
	}
}

// checkBank stops the run when the bank is tombstoned or gone (a job never writes to a deleting bank).
func checkBank(ctx context.Context, tx store.Tx, b domain.BankRef) error {
	bank, err := tx.Banks().Get(ctx, b)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: bank %s is gone: %w", domain.ErrCancelled, b, err)
	}
	if err != nil {
		return err
	}
	if bank.Deleting() {
		return fmt.Errorf("%w: bank %s is being deleted", domain.ErrCancelled, b)
	}
	return nil
}

// topUpLinks takes up to limit units from graph_dirty, lets the linker add the temporal and semantic links
// they lack (it fills up to the per-unit caps and keeps existing edges), and clears them from the queue
// whether or not anything was added. It returns how many queue entries it consumed.
func (s *Service) topUpLinks(ctx context.Context, tx store.Tx, b domain.BankRef, limit int) (int, error) {
	ids, err := tx.Units().DirtyLinks(ctx, b, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	units, err := tx.GetUnits(ctx, b, ids)
	if err != nil {
		return 0, err
	}
	if len(units) > 0 {
		if err := s.d.Linker.Link(ctx, tx, b, units, nil); err != nil {
			return 0, fmt.Errorf("top up links of %d units: %w", len(units), err)
		}
	}
	if err := tx.Units().ClearDirtyLinks(ctx, b, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// pruneEntities takes up to limit entities from entity_maintenance and deletes those no live unit
// references (with their co-occurrences); PruneOrphans removes every examined id from the queue. It
// returns how many queue entries it consumed.
func pruneEntities(ctx context.Context, tx store.Tx, b domain.BankRef, limit int) (int, error) {
	ids, err := tx.Entities().PendingMaintenance(ctx, b, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := tx.Entities().PruneOrphans(ctx, b, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}
