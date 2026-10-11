package mentalmodel

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
)

// The reads below are store.Reader methods (ScopeReader, ModelLister) and store.Maintenance methods (CronSource,
// FailureSource). The engine declares the small subsets it consumes; the store satisfies each of them.

// ScopeReader answers "has anything in this scope changed?" without a model call, and lists the bank's tags
// so fuzzy tag leaves resolve to exact tags before the scope compiles (decision 1203).
type ScopeReader interface {
	// NewestSeqInScope returns the highest seq of a live unit matching q, or 0 when none matches.
	NewestSeqInScope(ctx context.Context, q store.ScopeQuery) (int64, error)
	// TagCounts returns up to limit (<= 0 = all) tags of b's source with how many items carry each.
	// Unknown or tombstoned bank: domain.ErrNotFound.
	TagCounts(
		ctx context.Context, b domain.BankRef, source domain.TagSource, prefix, after string, limit int,
	) ([]domain.TagCount, error)
}

// ModelLister lists a bank's mental models, ordered by id.
type ModelLister interface {
	// ListMentalModels returns one page of models matching q (with content) and the next page token
	// ("" = last). Unknown or tombstoned bank: domain.ErrNotFound.
	ListMentalModels(ctx context.Context, q store.ModelQuery) ([]domain.MentalModel, string, error)
}

// CronSource finds models with a cron schedule across all banks.
type CronSource interface {
	// CronModels returns every model with refresh_cron set whose bank is not tombstoned.
	CronModels(ctx context.Context) ([]store.CronModel, error)
}

// FailureSource finds failed refreshes across all banks.
type FailureSource interface {
	// FailedRefreshes returns, per model, its latest COMPLETED or FAILED refresh_mental_model operation
	// (CANCELLED ones are ignored) when that operation is FAILED, on banks that are not tombstoned.
	// Operations of deleted models are skipped.
	FailedRefreshes(ctx context.Context) ([]store.FailedRefresh, error)
}
