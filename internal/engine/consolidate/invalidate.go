package consolidate

import (
	"context"
	"fmt"
	"memd/internal/port"
)

// Invalidator clears derived observations and resets their sources for re-consolidation. Every method
// runs in the caller's transaction; ClearForMemory and ClearDocumentUnits enqueue the follow-up
// consolidate through tx.Queue(), ClearScopes leaves that to its caller (decision 1005).
type Invalidator interface {
	// ClearForMemory deletes the observations citing unit, resets consolidated_at on their other sources
	// and returns the affected scopes.
	ClearForMemory(ctx context.Context, tx port.Tx, b port.BankRef, unit port.UnitID) ([]port.ScopeKey, error)
	// ClearScopes deletes every observation in scopes (every scope of b when scopes is empty: the RPC's
	// "empty = all", mapped to EvidenceRepo.DeleteAllObservations), resets their sources and returns the
	// deleted observations. It enqueues nothing. Callers must pass only scopes the client named, never a
	// list derived from data, which could be empty.
	ClearScopes(ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey) ([]port.UnitID, error)
	// ClearDocumentUnits clears the observations citing any unit of doc, resets their co-sources and
	// returns the affected scopes.
	ClearDocumentUnits(ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID) ([]port.ScopeKey, error)
}

// invalidator implements Invalidator over the transaction's repositories.
type invalidator struct{}

var _ Invalidator = (*invalidator)(nil)

// NewInvalidator returns the Invalidator. Each method takes tx.Seq (a clear changes memory content) before
// it reads, deletes or enqueues. ClearForMemory and ClearDocumentUnits enqueue the bank's automatic
// consolidate job (DedupePending) when they reset or deleted anything. Refreshes of models whose source_ids
// include a removed unit are the curator's (it knows what it removed); stale models in the affected scopes
// refresh through the next consolidation.
func NewInvalidator() Invalidator {
	return &invalidator{}
}

// ClearForMemory reads the scopes of the observations citing unit, deletes them and resets their other
// sources.
func (v *invalidator) ClearForMemory(
	ctx context.Context, tx port.Tx, b port.BankRef, unit port.UnitID,
) ([]port.ScopeKey, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear observations: %w", err)
	}
	return v.clearCiting(ctx, tx, b, []port.UnitID{unit})
}

// ClearDocumentUnits clears the observations citing any live unit of doc (UnitRepo.IDsByDocument).
func (v *invalidator) ClearDocumentUnits(
	ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID,
) ([]port.ScopeKey, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear observations: %w", err)
	}
	ids, err := tx.Units().IDsByDocument(ctx, b, doc)
	if err != nil {
		return nil, fmt.Errorf("consolidate: units of document %s: %w", doc, err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return v.clearCiting(ctx, tx, b, ids)
}

// ClearScopes deletes the scopes' observations and resets their sources. The caller (ClearObservations,
// with or without reconsolidate) owns the consolidate job. An empty scopes list is the RPC's "every scope"
// and maps explicitly to DeleteAllObservations; DeleteScopes itself deletes nothing for an empty list.
func (v *invalidator) ClearScopes(
	ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey,
) ([]port.UnitID, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	var deleted []port.UnitID
	var err error
	if len(scopes) == 0 {
		deleted, _, err = tx.Evidence().DeleteAllObservations(ctx, b)
	} else {
		deleted, _, err = tx.Evidence().DeleteScopes(ctx, b, scopes)
	}
	if err != nil {
		return nil, fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	return deleted, nil
}

// clearCiting deletes the observations citing ids and returns their scopes. The caller has taken tx.Seq.
func (v *invalidator) clearCiting(
	ctx context.Context, tx port.Tx, b port.BankRef, ids []port.UnitID,
) ([]port.ScopeKey, error) {
	scopes, err := tx.Evidence().CitingScopes(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("consolidate: scopes citing %v: %w", ids, err)
	}
	deleted, reset, err := tx.Evidence().DeleteCiting(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("consolidate: delete observations citing %v: %w", ids, err)
	}
	if len(deleted)+len(reset) == 0 {
		return scopes, nil
	}
	if _, _, err := tx.Queue().Enqueue(ctx, NewJob(b, nil, nil)); err != nil {
		return nil, fmt.Errorf("consolidate: enqueue consolidation after a clear: %w", err)
	}
	return scopes, nil
}
