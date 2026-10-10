package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// ObservationAdmin runs and clears consolidation. Its method set mirrors server.ObservationAdmin with the
// page and result types declared here (decision 1302).
type ObservationAdmin interface {
	// Consolidate queues a manual consolidation over scopes (all when empty) with request overrides and
	// returns its operation. It is a write: an unknown bank is created, a tombstoned one is
	// domain.ErrBankDeleting.
	Consolidate(
		ctx context.Context, b port.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
	) (domain.Operation, error)
	// ListScopes returns one page of the bank's observation scopes with their counts.
	ListScopes(ctx context.Context, b port.BankRef, pageSize int, pageToken string) (ScopePage, error)
	// ClearObservations deletes the observations of scopes (all when empty), resets their sources and,
	// when reconsolidate, queues consolidation.
	ClearObservations(
		ctx context.Context, b port.BankRef, scopes []domain.TagSet, reconsolidate bool,
	) (ClearResult, error)
	// ClearMemoryObservations deletes the observations citing one memory, resets their co-sources and,
	// when reconsolidate, queues consolidation.
	ClearMemoryObservations(
		ctx context.Context, b port.BankRef, id port.UnitID, reconsolidate bool,
	) (ClearResult, error)
}

// ScopeSource reads observation scopes. It is the port.Reader method proposed in CCR WP-13-1; port.Reader
// satisfies it once the CCR lands.
type ScopeSource interface {
	// ListScopes returns up to limit of b's observation scopes (every scope holding an observation or an
	// unconsolidated unit) with their counts, ordered by ScopeKey.String() and starting strictly after
	// after ("" = from the start). An unknown or tombstoned bank is domain.ErrNotFound.
	ListScopes(ctx context.Context, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error)
}

// ObservationClearer is the part of consolidate.Invalidator ClearObservations consumes;
// consolidate.Invalidator satisfies it.
type ObservationClearer interface {
	// ClearScopes deletes every observation in scopes (all scopes when empty) and resets their sources,
	// in the caller's transaction.
	ClearScopes(ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey) error
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
	Store port.Store
	// Scopes lists scopes (and counts the observations a clear removes).
	Scopes ScopeSource
	// Clearer deletes the observations of scopes.
	Clearer ObservationClearer
}
