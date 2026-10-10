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
