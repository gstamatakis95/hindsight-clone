package memory

import (
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/linking"
	"memd/internal/store"
	"slices"
	"time"
)

// EditField names a curated field of a fact that UpdateMemory can change; its value is the proto field name.
type EditField string

// The fields UpdateMemory can change.
const (
	// EditText is the fact text.
	EditText EditField = "text"
	// EditContext is the fact context.
	EditContext EditField = "context"
	// EditOccurredStart is when the fact started happening.
	EditOccurredStart EditField = "occurred_start"
	// EditOccurredEnd is when the fact stopped happening.
	EditOccurredEnd EditField = "occurred_end"
	// EditFactType is the fact type (world or experience).
	EditFactType EditField = "fact_type"
	// EditEntities are the entity names linked to the fact.
	EditEntities EditField = "entities"
)

// editFields are the valid EditField values.
var editFields = []EditField{
	EditText, EditContext, EditOccurredStart, EditOccurredEnd, EditFactType, EditEntities,
}

// Edit holds the new values of an UpdateMemory. Only the fields UpdateRequest.Fields names apply; a named
// field left zero clears it (an empty text and a fact type other than world or experience are rejected).
type Edit struct {
	// Text is the new fact text.
	Text string
	// Context is the new context; empty clears it.
	Context string
	// OccurredStart is the new occurrence start; nil clears it.
	OccurredStart *time.Time
	// OccurredEnd is the new occurrence end; nil clears it.
	OccurredEnd *time.Time
	// FactType is the new fact type: world or experience.
	FactType domain.FactType
	// Entities are the new entity names; empty removes every entity.
	Entities []string
}

// UpdateRequest mirrors memory.v1.UpdateMemoryRequest with domain types: an edit of a live fact.
type UpdateRequest struct {
	// Bank owns the unit.
	Bank domain.BankRef
	// ID is the fact to edit (observations are rejected).
	ID domain.UnitID
	// Edit holds the new values.
	Edit Edit
	// Fields names the fields of Edit to apply; empty = nothing to update (domain.ErrInvalidArgument).
	Fields []EditField
	// ResolveEntities false takes Edit.Entities literally; nil = true.
	ResolveEntities *bool
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
	// ResolveEntities false re-attaches the archived entity names literally; nil = true.
	ResolveEntities *bool
	// Overrides are kept on the rederive operation; nil = none.
	Overrides *domain.BankConfig
}

// action is what one curation does.
type action int

// The curation actions.
const (
	// actionEdit applies an UpdateMemory edit.
	actionEdit action = iota
	// actionInvalidate archives the fact.
	actionInvalidate
	// actionRestore brings an archived fact back.
	actionRestore
)

// curation is one curation call: the edit, invalidate or restore request in UpdateRequest form (Fields empty
// unless editing) and its action.
type curation struct {
	UpdateRequest

	act action
}

// edits reports whether the curation changes field f.
func (c curation) edits(f EditField) bool { return slices.Contains(c.Fields, f) }

// hasEdits reports whether the curation changes any curated field.
func (c curation) hasEdits() bool { return len(c.Fields) > 0 }

// curatorDeps are the curator's dependencies.
type curatorDeps struct {
	// Store opens the RPC's transaction, whose reads find the mental models a curation must refresh
	// (ModelsCiting). Its Read() reads an archived unit's snapshot entity names before a restore, so the
	// restore re-creates entities graph maintenance pruned meanwhile.
	Store store.DB
	// Clock stamps edited_at, audit rows and events.
	Clock clock.Clock
	// Resolver resolves an edit's entity names and a restore's snapshot names inside the RPC's transaction.
	Resolver *linking.Resolver
	// Config resolves the entity match threshold (with the request overrides) before the transaction.
	Config *config.Resolver
}
