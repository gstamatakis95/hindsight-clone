package observation

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
)

// batchError marks a failed CONSOLIDATE call: the batch is halved, and a unit that fails alone is
// deferred until settle. Every other error (reads, writes, embeddings, routing) stops the run and the job
// retries.
type batchError struct{ err error }

// Error returns the call's error text.
func (e *batchError) Error() string { return "consolidate call: " + e.err.Error() }

// Unwrap returns the call's error.
func (e *batchError) Unwrap() error { return e.err }

// batch decides ids in scope i and applies the decision. A failed call halves the batch; a unit failing
// alone is deferred, and the caller settles it once the top-level batch is done (decision 1002).
func (r *run) batch(ctx context.Context, i int, cfg domain.BankConfig, ids []domain.UnitID) error {
	if len(ids) == 0 {
		return nil
	}
	be, err := r.attempt(ctx, i, cfg, ids)
	if err != nil || be == nil {
		return err
	}
	if len(ids) == 1 {
		r.deferred = append(r.deferred, ids[0])
		return nil
	}
	mid := len(ids) / 2
	if err := r.batch(ctx, i, cfg, ids[:mid]); err != nil {
		return err
	}
	return r.batch(ctx, i, cfg, r.open(ids[mid:]))
}

// attempt decides ids outside any transaction, then applies the decision and stamps the units whose last
// scope this is in one short transaction. It returns the call's *batchError when CONSOLIDATE failed, and
// any other error as err.
func (r *run) attempt(ctx context.Context, i int, cfg domain.BankConfig, ids []domain.UnitID) (*batchError, error) {
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
			return nil, err
		}
		r.lastErr = be.err
		return be, nil
	}
	var stamp []domain.UnitID
	for _, id := range ids {
		if r.last[id] == i {
			stamp = append(stamp, id)
		}
	}
	out, err := r.c.apply(ctx, applyInput{
		scope: w.key, actions: dec.actions, incomplete: dec.incomplete, batch: ids, stamp: stamp,
		expect: r.units, cited: r.cited, refresh: r.reaching(w.key), mm: r.cfg.MentalModels,
	})
	if err != nil {
		return nil, err
	}
	r.treeOK, r.deadTrees = true, 0
	r.processed += len(out.stamped)
	for _, id := range out.unsettled {
		r.unsettled[id] = true
	}
	if out.wrote {
		r.changed[w.key.String()] = w.key
	}
	r.recordCited(w.key, out.cited)
	return nil, nil
}

// recordCited adds scope s to the cited scopes of each of ids, the facts an observation this run wrote in s
// cites, so later stamps re-check that s still cites them (decision 1801).
func (r *run) recordCited(s domain.ScopeKey, ids []domain.UnitID) {
	for _, id := range ids {
		if _, planned := r.units[id]; !planned {
			continue
		}
		if !slices.ContainsFunc(r.cited[id], func(k domain.ScopeKey) bool { return k.String() == s.String() }) {
			r.cited[id] = append(r.cited[id], s)
		}
	}
}

// settle resolves the units deferred by scope i's last top-level batch, with that scope's config and so
// its CONSOLIDATE binding (decision 1002). A health probe (one call with no facts) must succeed first;
// otherwise the run fails with UNAVAILABLE and nothing is parked. Each deferred unit is then retried
// alone once, and only those that fail again are parked. Two top-level batches in a row in which every
// call failed, with no success between them, trip the circuit breaker: the run fails instead.
func (r *run) settle(ctx context.Context, i int, cfg domain.BankConfig) error {
	if len(r.deferred) == 0 {
		return nil
	}
	deferred := r.deferred
	if r.deadTrees >= 2 {
		return fmt.Errorf("%w: consolidate: two batches in a row failed on every call (%v deferred): %w",
			domain.ErrUnavailable, deferred, r.lastErr)
	}
	usage, err := r.c.probe(ctx, r.scopes[i].key, cfg)
	r.addUsage(usage)
	if err != nil {
		if errStops(ctx, err) {
			return fmt.Errorf("consolidate: health probe: %w", err)
		}
		return fmt.Errorf("%w: consolidate: %v failed alone and the health probe failed (%w); last failure: %w",
			domain.ErrUnavailable, deferred, err, r.lastErr)
	}
	r.deferred = nil
	var poison []domain.UnitID
	for _, id := range deferred {
		be, err := r.attempt(ctx, i, cfg, []domain.UnitID{id})
		if err != nil {
			return err
		}
		if be != nil {
			poison = append(poison, id)
		}
	}
	if len(poison) == 0 {
		return nil
	}
	return r.park(ctx, poison)
}

// park stamps consolidation_failed_at on those of ids still unchanged since the plan, so they no longer
// block the bank. It takes tx.Seq first, like apply, so no edit can slip between the re-read and the
// stamp; a unit edited or retagged meanwhile is left for the next run instead.
func (r *run) park(ctx context.Context, ids []domain.UnitID) error {
	at := r.c.d.Clock.Now()
	var parked []domain.UnitID
	err := r.c.d.DB.InTx(ctx, func(tx store.Tx) error {
		parked = nil
		if _, err := tx.Seq(ctx, r.job.Bank); err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		live, err := liveFacts(ctx, tx, r.job.Bank, ids, r.units)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, ok := live[id]; ok {
				parked = append(parked, id)
			}
		}
		if len(parked) == 0 {
			return nil
		}
		return tx.Units().MarkConsolidationFailed(ctx, parked, at)
	})
	if err != nil {
		return fmt.Errorf("consolidate: park %v: %w", ids, err)
	}
	for _, id := range ids {
		r.parked[id] = true // parked, or changed: either way it is out of this run
	}
	r.processed += len(parked)
	return nil
}
