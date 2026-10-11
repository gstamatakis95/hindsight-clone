package consolidate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Invalidator clears derived observations and resets their sources for re-consolidation. It runs in the
// caller's transaction and leaves the follow-up consolidate to its caller (decision 1005).
type Invalidator interface {
	// ClearScopes deletes every observation in scopes (every scope of b when scopes is empty: the RPC's
	// "empty = all", mapped to EvidenceRepo.DeleteAllObservations), resets their sources and returns the
	// deleted observations. It enqueues nothing. Callers must pass only scopes the client named, never a
	// list derived from data, which could be empty.
	ClearScopes(ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.ScopeKey) ([]domain.UnitID, error)
}

// invalidator implements Invalidator over the transaction's repositories.
type invalidator struct{}

var _ Invalidator = (*invalidator)(nil)

// NewInvalidator returns the Invalidator. ClearScopes takes tx.Seq (a clear changes memory content) before
// it deletes. Stale models in the cleared scopes refresh through the next consolidation.
func NewInvalidator() Invalidator {
	return &invalidator{}
}

// ClearScopes deletes the scopes' observations and resets their sources. The caller (ClearObservations,
// with or without reconsolidate) owns the consolidate job. An empty scopes list is the RPC's "every scope"
// and maps explicitly to DeleteAllObservations; DeleteScopes itself deletes nothing for an empty list.
func (v *invalidator) ClearScopes(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.ScopeKey,
) ([]domain.UnitID, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	var deleted []domain.UnitID
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
