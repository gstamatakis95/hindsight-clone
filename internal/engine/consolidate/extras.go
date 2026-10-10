package consolidate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// The interfaces below are reads and writes consolidation needs that the frozen ports lack. They are
// declared here by their consumer; CCR WP-10-1 proposes the matching port methods, and WP-14 wires an
// implementation (the Postgres adapter, then the fake) until the CCR lands.

// ObservationCounter counts a scope's observations for max_observations_per_scope.
type ObservationCounter interface {
	// CountObservations returns how many live observations scope s holds (its exact tag set).
	CountObservations(ctx context.Context, s port.ScopeKey) (int, error)
}

// ScopeLister lists a bank's observation scopes, for StrategyMatcher.Preview.
type ScopeLister interface {
	// Scopes returns up to limit of b's observation scopes and whether that is all of them.
	Scopes(ctx context.Context, b port.BankRef, limit int) ([]port.ScopeKey, bool, error)
}

// ModelLister lists a bank's mental models, for the refresh triggers after a consolidation run.
type ModelLister interface {
	// MentalModels returns every mental model of b.
	MentalModels(ctx context.Context, b port.BankRef) ([]domain.MentalModel, error)
}

// ObservationClearer holds the transactional observation reads and deletes the Invalidator needs. Every
// method runs in the caller's transaction tx.
type ObservationClearer interface {
	// CitingScopes returns the distinct scopes of b's observations that cite any of ids.
	CitingScopes(ctx context.Context, tx port.Tx, b port.BankRef, ids []port.UnitID) ([]port.ScopeKey, error)
	// DeleteScopes deletes every observation of b in scopes (every scope when empty), clears consolidated_at
	// on their live sources and returns the deleted observations and the reset sources.
	DeleteScopes(
		ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey,
	) (deleted, reset []port.UnitID, err error)
	// DocumentUnits returns the ids of doc's live units.
	DocumentUnits(ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID) ([]port.UnitID, error)
}

// Checkpointer reports a run's progress and stops it once its operation is cancelled or its bank is
// deleted; worker.Checkpoint satisfies it.
type Checkpointer interface {
	// Report records p and returns an error wrapping domain.ErrCancelled when the run must stop.
	Report(ctx context.Context, p domain.Progress) error
}
