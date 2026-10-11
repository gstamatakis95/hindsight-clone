package memory

import (
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/linking"
	"memd/internal/store"
	"time"
)

// UpdateRequest mirrors memory.v1.UpdateMemoryRequest with domain types. Unset fields are left unchanged;
// State = invalidated archives the unit and State = valid restores an archived one.
type UpdateRequest struct {
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

// RestoreRequest asks RestoreMemory to bring an archived fact back.
type RestoreRequest struct {
	// Bank owns the unit.
	Bank domain.BankRef
	// ID is the archived fact.
	ID domain.UnitID
	// Reason is recorded on the audit row.
	Reason string
	// Overrides are kept on the rederive operation; nil = none.
	Overrides *domain.BankConfig
}

// curatorDeps are the curator's dependencies.
type curatorDeps struct {
	// Store opens the RPC's transaction. Its Read() finds the mental models an invalidate must refresh
	// (ModelsCiting, in the invalidate's transaction through jobs.QueueModelRefresh) and reads an archived
	// unit's snapshot entity names before a restore, so the restore re-creates entities graph maintenance
	// pruned meanwhile.
	Store store.DB
	// Clock stamps edited_at, audit rows and events.
	Clock clock.Clock
	// Resolver resolves an edit's entity names and a restore's snapshot names inside the RPC's transaction.
	Resolver *linking.Resolver
	// Config resolves the entity match threshold (with the request overrides) before the transaction.
	Config *config.Resolver
}
