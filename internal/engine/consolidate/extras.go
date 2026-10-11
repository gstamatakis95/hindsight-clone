package consolidate

import (
	"context"
	"memd/internal/domain"
)

// ScopeLister lists a bank's observation scopes, for StrategyMatcher.Preview. It is the store.Reader method;
// store.Reader satisfies it.
type ScopeLister interface {
	// ListScopes returns up to limit of b's observation scopes ordered by ScopeKey.String(), starting strictly
	// after after ("" = from the start). Unknown or tombstoned bank: domain.ErrNotFound.
	ListScopes(ctx context.Context, b domain.BankRef, after string, limit int) ([]domain.ScopeInfo, error)
}

// Checkpointer reports a run's progress and stops it once its operation is cancelled or its bank is
// deleted; worker.Checkpoint satisfies it.
type Checkpointer interface {
	// Report records p and returns an error wrapping domain.ErrCancelled when the run must stop.
	Report(ctx context.Context, p domain.Progress) error
}
