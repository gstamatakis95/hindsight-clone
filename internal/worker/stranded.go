package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/curate"
	"memd/internal/port"
	"time"
)

// Defaults of the stranded-fact reconcile.
const (
	// DefaultStrandedRetryAfter is how long after a rederive ended the reconcile may queue another one for
	// the same unit, so a fact whose rederive keeps failing is retried every few minutes, not every tick.
	DefaultStrandedRetryAfter = 10 * time.Minute
	// strandedBatch caps the rederives one tick enqueues.
	strandedBatch = 100
)

// StrandedFactSource finds live facts left out of semantic search and consolidation because their
// rederive_memory ended FAILED or CANCELLED: an edit or restore clears the embedding and keeps the
// consolidation stamp until rederive writes the new embedding (decision 1305), so a rederive that fails for
// good strands the fact. The Postgres adapter implements it (decision 1452).
type StrandedFactSource interface {
	// StrandedFacts returns up to limit such facts (Bank, ID and DocumentID set) on banks that are not
	// tombstoned, skipping units with a rederive that is pending, processing or ended less than retryAfter
	// ago.
	StrandedFacts(ctx context.Context, retryAfter time.Duration, limit int) ([]domain.Unit, error)
}

// rederiveStranded re-enqueues rederive_memory for stranded facts, one short transaction per fact. The
// source skips units whose rederive is queued or running, so ticks don't pile jobs up; two workers ticking
// at once may queue a unit twice, which is harmless (rederive is idempotent). It returns how many it queued.
func rederiveStranded(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Stranded == nil {
		return 0, nil
	}
	units, err := d.Stranded.StrandedFacts(ctx, d.StrandedRetryAfter, strandedBatch)
	if err != nil {
		return 0, fmt.Errorf("list stranded facts: %w", err)
	}
	n := 0
	var errs []error
	for _, u := range units {
		err := enqueueRederive(ctx, d.Store, u)
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

// enqueueRederive enqueues rederive_memory for u with the payload and serial key curate uses (decision
// 1301): its document's key, so it never overlaps a retain of that document.
func enqueueRederive(ctx context.Context, s port.Store, u domain.Unit) error {
	payload, err := json.Marshal(curate.RederivePayload{UnitID: u.ID})
	if err != nil {
		return fmt.Errorf("encode rederive payload: %w", err)
	}
	key := "doc:" + string(u.DocumentID)
	if u.DocumentID == "" {
		key = "unit:" + string(u.ID)
	}
	return s.InTx(ctx, func(tx port.Tx) error {
		_, _, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank: u.Bank, Kind: domain.OperationKindRederiveMemory, SerialKey: key, Payload: payload,
			Dedupe: port.DedupeNone,
		})
		return err
	})
}
