package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/retain"
	"memd/internal/port"
)

// Rederiver is the rederive_memory job: re-embed, re-resolve, re-link, enqueue consolidation.
type Rederiver interface {
	// Rederive re-embeds the job's unit, re-resolves its entities (or takes names literally), rebuilds
	// its temporal and semantic links and enqueues consolidation. A unit that is gone (archived or
	// replaced) or whose text changed after the embedding was computed is left alone: the job succeeds
	// and a later rederive (serialized behind it) does the work. Running it twice is harmless.
	Rederive(ctx context.Context, j port.Job) error
}

// RederivePayload is the rederive_memory job payload (decision 1301).
type RederivePayload struct {
	// UnitID is the edited or restored unit.
	UnitID port.UnitID `json:"unit_id"`
	// Entities replaces the unit's entity names; nil (null) = re-resolve the unit's current names, non-nil
	// empty ([]) = clear them. No omitempty: it would turn a clear into "keep".
	Entities []string `json:"entities"`
	// ResolveEntities false takes Entities literally; nil (absent) = true.
	ResolveEntities *bool `json:"resolve_entities,omitempty"`
}

// EntityResolver is the part of retain.EntityResolver the rederiver consumes; retain.EntityResolver
// satisfies it.
type EntityResolver interface {
	// Resolve maps the mentions of one fact to existing or new entities inside tx.
	Resolve(ctx context.Context, tx port.Tx, b port.BankRef, in retain.ResolveInput) ([]port.EntityID, error)
}

// Linker is the part of retain.Linker the rederiver consumes; retain.Linker satisfies it.
type Linker interface {
	// Link adds temporal + semantic (+ causal) edges for units within the per-unit caps, in tx.
	Link(ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes retain.CauseMap) error
}

// RederiverDeps are the Rederiver's dependencies.
type RederiverDeps struct {
	// Store opens the job's short write transaction.
	Store port.Store
	// Embedder re-embeds the unit text, outside any transaction.
	Embedder port.Embedder
	// Resolver re-resolves the unit's entity names.
	Resolver EntityResolver
	// Linker rebuilds the unit's temporal and semantic links.
	Linker Linker
	// Config resolves the effective config (entity match threshold) with the job's overrides.
	Config port.ConfigResolver
}
