package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/retain"
	"memd/internal/port"
)

// Rederiver is the rederive_memory job: re-embed, re-link, enqueue consolidation.
type Rederiver interface {
	// Rederive re-embeds the job's unit, rebuilds its temporal and semantic links and enqueues
	// consolidation. Entity changes are not its job: the Curator resolves an edit's names and a restore's
	// snapshot names in the RPC's own transaction, so a rederive keeps the unit's current attachments and an
	// older rederive retried after a newer edit can never re-apply stale names (decision 1305). A unit that
	// is gone (archived or replaced) or whose text changed after the embedding was computed is left alone:
	// the job succeeds and the newer edit's rederive does the work. Running it twice is harmless.
	Rederive(ctx context.Context, j port.Job) error
}

// RederivePayload is the rederive_memory job payload (decision 1301).
type RederivePayload struct {
	// UnitID is the edited or restored unit. The payload carries no entity names: the RPC committed them.
	UnitID port.UnitID `json:"unit_id"`
}

// EntityResolver is the part of retain.EntityResolver the curator consumes (no model call, so it runs
// inside the RPC's transaction); retain.EntityResolver
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
	// Linker rebuilds the unit's temporal and semantic links.
	Linker Linker
}
