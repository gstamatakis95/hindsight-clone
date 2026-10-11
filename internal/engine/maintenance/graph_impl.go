package maintenance

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Batch sizes of one graph_maintenance transaction.
const (
	// DirtyBatch is how many graph_dirty units one batch tops up.
	DirtyBatch = 100
	// EntityBatch is how many entity_maintenance entries one batch examines.
	EntityBatch = 500
)

// graphMaintainer implements GraphMaintainer.
type graphMaintainer struct {
	s      port.Store
	linker Linker
	clock  port.Clock
}

var _ GraphMaintainer = (*graphMaintainer)(nil)

// NewGraphMaintainer returns the graph_maintenance job body. linker tops up links (retain.Linker); clock
// measures the budget, so tests drive it with a fake clock.
func NewGraphMaintainer(s port.Store, linker Linker, clock port.Clock) GraphMaintainer {
	return &graphMaintainer{s: s, linker: linker, clock: clock}
}

// Run commits one batch per transaction (up to DirtyBatch link top-ups plus up to EntityBatch entity
// prunes) until a batch finds both queues empty (done=true) or the budget has run out (done=false). done is
// only ever reported by a batch that found both queues empty after the last batch that did work, so work
// queued while a batch ran (even inside it) is drained before the run ends. The
// budget is checked between batches only, so a batch is never cut in half. Neither step takes the bank's
// change seq: links and entity rows are not memory content, so models must not go stale over them.
func (g *graphMaintainer) Run(ctx context.Context, b port.BankRef, budget time.Duration) (bool, error) {
	deadline := g.clock.Now().Add(budget)
	for {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: graph maintenance of %s/%s: %w", domain.ErrCancelled, b.Tenant, b.Bank, err)
		}
		var linked, pruned int
		err := g.s.InTx(ctx, func(tx port.Tx) error {
			linked, pruned = 0, 0
			if err := checkBank(ctx, tx, b); err != nil {
				return err
			}
			var err error
			if linked, err = g.topUpLinks(ctx, tx, b, DirtyBatch); err != nil {
				return err
			}
			pruned, err = g.pruneEntities(ctx, tx, b, EntityBatch)
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return false, fmt.Errorf("%w: graph maintenance of %s/%s: %w", domain.ErrCancelled, b.Tenant, b.Bank, err)
			}
			return false, err
		}
		if linked == 0 && pruned == 0 {
			return true, nil
		}
		if !g.clock.Now().Before(deadline) {
			return false, nil
		}
	}
}

// checkBank stops the run when the bank is tombstoned or gone (a job never writes to a deleting bank).
func checkBank(ctx context.Context, tx port.Tx, b port.BankRef) error {
	bank, err := tx.Banks().Get(ctx, b)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: bank %s/%s is gone: %w", domain.ErrCancelled, b.Tenant, b.Bank, err)
	}
	if err != nil {
		return err
	}
	if bank.Deleting() {
		return fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrCancelled, b.Tenant, b.Bank)
	}
	return nil
}

// topUpLinks takes up to limit units from graph_dirty, lets the linker add the temporal and semantic links
// they lack (it fills up to the per-unit caps and keeps existing edges), and clears them from the queue
// whether or not anything was added. It returns how many queue entries it consumed.
func (g *graphMaintainer) topUpLinks(ctx context.Context, tx port.Tx, b port.BankRef, limit int) (int, error) {
	ids, err := tx.Links().Dirty(ctx, b, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	units, err := tx.GetUnits(ctx, b, ids)
	if err != nil {
		return 0, err
	}
	if len(units) > 0 {
		if err := g.linker.Link(ctx, tx, b, units, nil); err != nil {
			return 0, fmt.Errorf("top up links of %d units: %w", len(units), err)
		}
	}
	if err := tx.Links().ClearDirty(ctx, b, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// pruneEntities takes up to limit entities from entity_maintenance and deletes those no live unit
// references (with their co-occurrences); PruneOrphans removes every examined id from the queue. It
// returns how many queue entries it consumed.
func (g *graphMaintainer) pruneEntities(ctx context.Context, tx port.Tx, b port.BankRef, limit int) (int, error) {
	ids, err := tx.Entities().PendingMaintenance(ctx, b, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := tx.Entities().PruneOrphans(ctx, b, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}
