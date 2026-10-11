package consolidate

import (
	"context"
	"memd/internal/domain"
)

// ScopeLister lists a bank's observation scopes, for StrategyPreviewer.Preview. It is the store.Reader method;
// store.Reader satisfies it.
type ScopeLister interface {
	// ListScopes returns up to limit of b's observation scopes ordered by ScopeKey.String(), starting strictly
	// after after ("" = from the start). Unknown or tombstoned bank: domain.ErrNotFound.
	ListScopes(ctx context.Context, b domain.BankRef, after string, limit int) ([]domain.ScopeInfo, error)
}
