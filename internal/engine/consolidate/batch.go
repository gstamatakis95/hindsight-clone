package consolidate

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// batchError marks a failed CONSOLIDATE call: the batch is halved, and a unit that fails alone is parked.
// Every other error (reads, writes, embeddings, routing) stops the run and the job retries.
type batchError struct{ err error }

// Error returns the call's error text.
func (e *batchError) Error() string { return "consolidate call: " + e.err.Error() }

// Unwrap returns the call's error.
func (e *batchError) Unwrap() error { return e.err }

// batch decides ids in scope i outside any transaction, then applies the decision and stamps the units
// whose last scope this is in one short transaction. A failed call halves the batch; a unit failing alone
// is parked (or deferred until a call of the run succeeds).
func (r *run) batch(ctx context.Context, i int, cfg domain.BankConfig, ids []port.UnitID) error {
	if len(ids) == 0 {
		return nil
	}
	w := r.scopes[i]
	facts := make([]domain.Unit, 0, len(ids))
	for _, id := range ids {
		facts = append(facts, r.units[id])
	}
	dec, err := r.c.decide(ctx, w.key, cfg, facts)
	r.addUsage(dec.usage...)
	if err != nil {
		var be *batchError
		if errStops(ctx, err) || !errors.As(err, &be) {
			return err
		}
		r.lastErr = be.err
		if len(ids) == 1 {
			return r.failAlone(ctx, ids[0])
		}
		mid := len(ids) / 2
		if err := r.batch(ctx, i, cfg, ids[:mid]); err != nil {
			return err
		}
		return r.batch(ctx, i, cfg, r.unparked(ids[mid:]))
	}
	var stamp []port.UnitID
	for _, id := range ids {
		if r.last[id] == i {
			stamp = append(stamp, id)
		}
	}
	wrote, err := r.c.apply(ctx, applyInput{scope: w.key, actions: dec.actions, batch: ids, stamp: stamp})
	if err != nil {
		return err
	}
	r.succeeded = true
	r.processed += len(stamp)
	if wrote {
		r.changed[w.key.String()] = w.key
	}
	if len(r.deferred) > 0 {
		deferred := r.deferred
		r.deferred = nil
		return r.park(ctx, deferred)
	}
	return nil
}

// failAlone parks a unit whose call failed even alone, once a call of this run has succeeded (so the
// gateway works and the failure is the unit's); before that it is deferred.
func (r *run) failAlone(ctx context.Context, id port.UnitID) error {
	if r.succeeded {
		return r.park(ctx, []port.UnitID{id})
	}
	r.deferred = append(r.deferred, id)
	return nil
}

// park stamps consolidation_failed_at on ids so they no longer block the bank. Parking writes no memory
// content, so it takes no change seq.
func (r *run) park(ctx context.Context, ids []port.UnitID) error {
	at := r.c.d.Clock.Now()
	err := r.c.d.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.Units().MarkConsolidationFailed(ctx, ids, at)
	})
	if err != nil {
		return fmt.Errorf("consolidate: park %v: %w", ids, err)
	}
	for _, id := range ids {
		r.parked[id] = true
	}
	r.processed += len(ids)
	return nil
}
