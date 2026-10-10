package mentalmodel

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// The reads below are not in internal/port yet (CCR WP-12-1). The engine declares what it consumes; WP-14
// wires a Postgres implementation once the CCR lands, and tests use stubs over internal/fake.

// ScopeQuery selects the live units (facts and observations) of one bank in a model's scope.
type ScopeQuery struct {
	// Bank is the bank searched.
	Bank port.BankRef
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *port.SQLPredicate
	// Seq bounds the units' seq.
	Seq port.SeqRange
}

// ScopeReader answers "has anything in this scope changed?" without a model call.
type ScopeReader interface {
	// NewestSeq returns the highest seq of a live unit matching q, or 0 when none matches.
	NewestSeq(ctx context.Context, q ScopeQuery) (int64, error)
}

// ModelQuery selects mental models of one bank for listing.
type ModelQuery struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// FolderPrefix keeps models whose folder_path equals it or lies under it ("a" matches "a" and "a/b",
	// not "ab"); empty = all.
	FolderPrefix string
	// Tags filters by model tags with TagsMatch semantics (domain.MatchTags); empty = no filter.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// RefreshAfterConsolidation keeps only models whose trigger sets refresh_after_consolidation.
	RefreshAfterConsolidation bool
	// PageSize caps the page; 0 = all.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// ModelLister lists a bank's mental models, ordered by id.
type ModelLister interface {
	// ListModels returns one page of models matching q (with content) and the next page token ("" = last).
	ListModels(ctx context.Context, q ModelQuery) ([]domain.MentalModel, string, error)
}

// CronModel is a model with a refresh_cron schedule, as the cron sweep sees it.
type CronModel struct {
	// Model is the model.
	Model domain.MentalModel
	// LastRefreshEnqueuedAt is the created_at of the model's newest refresh_mental_model operation (any
	// status); zero when it has none.
	LastRefreshEnqueuedAt time.Time
}

// CronSource finds models with a cron schedule across all banks.
type CronSource interface {
	// CronModels returns every model with refresh_cron set whose bank is not tombstoned.
	CronModels(ctx context.Context) ([]CronModel, error)
}

// FailedRefresh is a model whose latest terminal refresh_mental_model operation is FAILED.
type FailedRefresh struct {
	// Bank owns the model.
	Bank port.BankRef
	// ModelID is the model.
	ModelID port.MentalModelID
	// OperationID is the failed operation.
	OperationID port.OperationID
	// FailedAt is when the operation failed (its finished_at, or updated_at).
	FailedAt time.Time
	// Error is the operation's recorded error.
	Error string
}

// FailureSource finds failed refreshes across all banks.
type FailureSource interface {
	// FailedRefreshes returns, per model, its latest terminal refresh_mental_model operation when that
	// operation is FAILED, on banks that are not tombstoned. Operations of deleted models are skipped.
	FailedRefreshes(ctx context.Context) ([]FailedRefresh, error)
}
