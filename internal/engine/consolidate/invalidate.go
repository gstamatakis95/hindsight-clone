package consolidate

import (
	"context"
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
