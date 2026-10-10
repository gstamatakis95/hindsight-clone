package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Curator commits edits, invalidations and restores; see the Curation table for what each does in-tx.
type Curator interface {
	// Update rejects observations; writes an audit row; returns the rederive operation id (empty for
	// invalidate).
	Update(ctx context.Context, req UpdateMemoryRequest) (domain.Unit, port.OperationID, error)
}

// UpdateMemoryRequest mirrors memory.v1.UpdateMemoryRequest with domain types. Unset fields are left
// unchanged; State = invalidated archives the unit and State = valid restores an archived one.
type UpdateMemoryRequest struct {
	// Bank owns the unit.
	Bank port.BankRef
	// ID is the unit to curate (a fact; observations are rejected).
	ID port.UnitID
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

// CitingModels finds the mental models whose source_ids cite units. It is the port.Reader method proposed
// in CCR WP-13-1; port.Reader satisfies it once the CCR lands.
type CitingModels interface {
	// ModelsCiting returns the ids of b's mental models whose source_ids include any of ids, ordered by
	// id. An unknown or tombstoned bank is domain.ErrNotFound.
	ModelsCiting(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]port.MentalModelID, error)
}

// RefreshQueuer enqueues mental-model refreshes; the mental-model engine (WP-12) owns the refresh payload
// and its scheduling rules (decision 1303). bank.RefreshQueuer has the same method.
type RefreshQueuer interface {
	// QueueRefresh enqueues (or joins) a refresh of model id of bank b inside tx and returns its operation
	// id. The caller has already called tx.Seq(b).
	QueueRefresh(ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID) (port.OperationID, error)
}

// CuratorDeps are the Curator's dependencies.
type CuratorDeps struct {
	// Store opens the RPC's transaction.
	Store port.Store
	// Clock stamps edited_at, audit rows and events.
	Clock port.Clock
	// Citing finds the models an invalidate must refresh.
	Citing CitingModels
	// Refresh enqueues those refreshes in the invalidate's transaction.
	Refresh RefreshQueuer
}
