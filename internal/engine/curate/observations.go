package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
)

// ObservationAdmin runs and clears consolidation. Its method set mirrors server.ObservationAdmin with the
// page and result types declared here (decision 1302).
type ObservationAdmin interface {
	// Consolidate queues a manual consolidation over scopes (all when empty) with request overrides and
	// returns its operation. It is a write: an unknown bank is created, a tombstoned one is
	// domain.ErrBankDeleting.
	Consolidate(
		ctx context.Context, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
	) (domain.Operation, error)
	// ListScopes returns one page of the bank's observation scopes with their counts.
	ListScopes(ctx context.Context, b domain.BankRef, pageSize int, pageToken string) (ScopePage, error)
	// ClearObservations deletes the observations of scopes (all when empty), resets their sources and,
	// when reconsolidate, queues consolidation.
	ClearObservations(
		ctx context.Context, b domain.BankRef, scopes []domain.TagSet, reconsolidate bool,
	) (ClearResult, error)
	// ClearMemoryObservations deletes the observations citing one memory, resets their co-sources and,
	// when reconsolidate, queues consolidation.
	ClearMemoryObservations(
		ctx context.Context, b domain.BankRef, id domain.UnitID, reconsolidate bool,
	) (ClearResult, error)
}

// ScopeSource reads observation scopes. It is the store.Reader method; store.Reader satisfies it.
type ScopeSource interface {
	// ListScopes returns up to limit of b's observation scopes (its observation_scopes rows: every scope that
	// has held an observation) with their counts, ordered by ScopeKey.String() and starting strictly after
	// after ("" = from the start, shared scope included; "0" continues after the shared scope, decision
	// 1560). An unknown or tombstoned bank is domain.ErrNotFound.
	ListScopes(ctx context.Context, b domain.BankRef, after string, limit int) ([]domain.ScopeInfo, error)
}

// ObservationClearer is the part of consolidate.Invalidator ClearObservations consumes;
// consolidate.Invalidator satisfies it.
type ObservationClearer interface {
	// ClearScopes deletes every observation in scopes (all scopes when empty) and resets their sources,
	// in the caller's transaction, and returns the deleted observations. It enqueues nothing:
	// ObservationAdmin owns the consolidate enqueue (exactly one, only when reconsolidate is set).
	ClearScopes(ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.ScopeKey) ([]domain.UnitID, error)
}

// ConsolidatePayload is the payload of a manual consolidate job (decision 1301); an automatic one is {}.
type ConsolidatePayload struct {
	// Scopes limits the run to these scopes; empty (absent) = every scope.
	Scopes []domain.TagSet `json:"scopes,omitempty"`
}

// ScopePage is one page of observation scopes; server.ScopePage becomes an alias of it (decision 1302).
type ScopePage struct {
	// Items are the page's scopes.
	Items []domain.ScopeInfo
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ClearResult reports what a clear removed and the consolidation it queued; server.ClearResult becomes an
// alias of it (decision 1302).
type ClearResult struct {
	// Deleted counts the observations removed.
	Deleted int64
	// Operation is the queued consolidation; nil when reconsolidate was false.
	Operation *domain.Operation
}

// ObservationAdminDeps are the ObservationAdmin's dependencies.
type ObservationAdminDeps struct {
	// Store opens the transactions.
	Store store.DB
	// Scopes lists scopes (store.Reader).
	Scopes ScopeSource
	// Clearer deletes the observations of scopes.
	Clearer ObservationClearer
}
