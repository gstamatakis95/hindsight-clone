package curate

import (
	"context"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// Curator commits edits, invalidations and restores; see the Curation table for what each does in-tx.
type Curator interface {
	// Update rejects observations; writes an audit row; returns the rederive operation id (empty for
	// invalidate).
	Update(ctx context.Context, req UpdateMemoryRequest) (domain.Unit, domain.OperationID, error)
}

// UpdateMemoryRequest mirrors memory.v1.UpdateMemoryRequest with domain types. Unset fields are left
// unchanged; State = invalidated archives the unit and State = valid restores an archived one.
type UpdateMemoryRequest struct {
	// Bank owns the unit.
	Bank domain.BankRef
	// ID is the unit to curate (a fact; observations are rejected).
	ID domain.UnitID
	// Text replaces the unit text; nil = unchanged.
	Text *string
	// Context replaces the unit context; nil = unchanged.
	Context *string
	// OccurredStart replaces the occurrence start; nil = unchanged.
	OccurredStart *time.Time
	// OccurredEnd replaces the occurrence end; nil = unchanged.
	OccurredEnd *time.Time
	// ClearOccurred clears both occurrence bounds.
	ClearOccurred bool
	// FactType changes world <-> experience; unspecified = unchanged.
	FactType domain.FactType
	// Entities replaces the unit's entity names; nil = unchanged, non-nil empty = clear.
	Entities []string
	// ResolveEntities false takes Entities literally at rederive; nil = true.
	ResolveEntities *bool
	// State invalidates (archive) or restores the unit; unspecified = unchanged.
	State domain.MemoryState
	// Reason is recorded on the audit row.
	Reason string
	// Overrides are kept on the rederive operation; nil = none.
	Overrides *domain.BankConfig
}

// CitingModels finds the mental models whose source_ids cite units. It is the store.Reader method;
// store.Reader satisfies it.
type CitingModels interface {
	// ModelsCiting returns the ids of b's mental models whose source_ids include any of ids, ordered by id.
	// It is id-keyed: an unknown bank has none (empty, no error).
	ModelsCiting(ctx context.Context, b domain.BankRef, ids []domain.UnitID) ([]domain.MentalModelID, error)
}

// UnitGetter reads one unit, live or archived. It is UnitSource.GetUnit (a store.Reader method); for an
// archived unit, Entities are the names in its archive snapshot, even when the entity
// rows were pruned since.
type UnitGetter interface {
	// GetUnit returns one unit of b, live or archived; unknown = domain.ErrNotFound.
	GetUnit(ctx context.Context, b domain.BankRef, id domain.UnitID) (domain.Unit, error)
}

// CuratorDeps are the Curator's dependencies.
type CuratorDeps struct {
	// Store opens the RPC's transaction.
	Store store.DB
	// Clock stamps edited_at, audit rows and events.
	Clock clock.Clock
	// Citing finds the models an invalidate must refresh (jobs.QueueModelRefresh, in the invalidate's
	// transaction); nil = no refreshes.
	Citing CitingModels
	// Resolver resolves an edit's entity names and a restore's snapshot names inside the RPC's transaction;
	// nil rejects such requests with domain.ErrFailedPrecondition.
	Resolver EntityResolver
	// Config resolves the entity match threshold (with the request overrides) before the transaction.
	Config *config.Resolver
	// Archived reads an archived unit's snapshot entity names before a restore, so the rederive re-creates
	// entities graph maintenance pruned meanwhile; nil = rely on the entities Restore reattaches.
	Archived UnitGetter
}
