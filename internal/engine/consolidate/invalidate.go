package consolidate

import (
	"context"
	"fmt"
	"memd/internal/port"
)

// Invalidator clears derived observations and resets their sources for re-consolidation. Every method
// runs in the caller's transaction and enqueues follow-up jobs (consolidate, refreshes) through
// tx.Queue().
type Invalidator interface {
	// ClearForMemory deletes the observations citing unit, resets consolidated_at on their other sources
	// and returns the affected scopes.
	ClearForMemory(ctx context.Context, tx port.Tx, b port.BankRef, unit port.UnitID) ([]port.ScopeKey, error)
	// ClearScopes deletes every observation in scopes (all scopes when empty) and resets their sources.
	ClearScopes(ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey) error
	// ClearDocumentUnits clears the observations citing any unit of doc, resets their co-sources and
	// returns the affected scopes.
	ClearDocumentUnits(ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID) ([]port.ScopeKey, error)
}

// invalidator implements Invalidator.
type invalidator struct {
	obs ObservationClearer
}

var _ Invalidator = (*invalidator)(nil)

// NewInvalidator returns the Invalidator. obs supplies the observation reads and deletes the frozen
// ports lack (CCR WP-10-1). Each method takes tx.Seq (a clear changes memory content) before it deletes
// or enqueues, and enqueues the bank's automatic consolidate job (DedupePending) when it reset or deleted
// anything. Refreshes of models whose source_ids include a removed unit are the curator's (it knows what
// it removed); stale models in the affected scopes refresh through the next consolidation.
func NewInvalidator(obs ObservationClearer) Invalidator {
	return &invalidator{obs: obs}
}

// ClearForMemory reads the scopes of the observations citing unit, deletes them and resets their other
// sources.
func (v *invalidator) ClearForMemory(
	ctx context.Context, tx port.Tx, b port.BankRef, unit port.UnitID,
) ([]port.ScopeKey, error) {
	return v.clearCiting(ctx, tx, b, []port.UnitID{unit})
}

// ClearDocumentUnits clears the observations citing any live unit of doc.
func (v *invalidator) ClearDocumentUnits(
	ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID,
) ([]port.ScopeKey, error) {
	ids, err := v.obs.DocumentUnits(ctx, tx, b, doc)
	if err != nil {
		return nil, fmt.Errorf("consolidate: units of document %s: %w", doc, err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return v.clearCiting(ctx, tx, b, ids)
}

// ClearScopes deletes the scopes' observations and resets their sources.
func (v *invalidator) ClearScopes(ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey) error {
	if _, err := tx.Seq(ctx, b); err != nil {
		return fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	deleted, reset, err := v.obs.DeleteScopes(ctx, tx, b, scopes)
	if err != nil {
		return fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	return enqueueAfterClear(ctx, tx, b, len(deleted)+len(reset))
}

// clearCiting deletes the observations citing ids and returns their scopes.
func (v *invalidator) clearCiting(
	ctx context.Context, tx port.Tx, b port.BankRef, ids []port.UnitID,
) ([]port.ScopeKey, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear observations: %w", err)
	}
	scopes, err := v.obs.CitingScopes(ctx, tx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("consolidate: scopes citing %v: %w", ids, err)
	}
	deleted, reset, err := tx.Evidence().DeleteCiting(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("consolidate: delete observations citing %v: %w", ids, err)
	}
	if err := enqueueAfterClear(ctx, tx, b, len(deleted)+len(reset)); err != nil {
		return nil, err
	}
	return scopes, nil
}

// enqueueAfterClear queues the bank's automatic consolidate when a clear changed anything.
func enqueueAfterClear(ctx context.Context, tx port.Tx, b port.BankRef, changed int) error {
	if changed == 0 {
		return nil
	}
	if _, _, err := tx.Queue().Enqueue(ctx, NewJob(b, nil, nil)); err != nil {
		return fmt.Errorf("consolidate: enqueue consolidation after a clear: %w", err)
	}
	return nil
}
