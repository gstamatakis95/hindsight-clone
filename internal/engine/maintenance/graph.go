package maintenance

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// GraphMaintainer drains graph_dirty (link top-ups) and entity_maintenance (orphan pruning) in batches.
type GraphMaintainer interface {
	// Run works on bank b in short committed batches until both queues are empty (done=true) or budget,
	// measured on the injected clock between batches, runs out with work left (done=false; the handler
	// re-enqueues with DedupePending). Every batch is idempotent, so a run that repeats after a reclaim
	// only redoes uncleared rows. A tombstoned or deleted bank stops the run with domain.ErrCancelled.
	Run(ctx context.Context, b port.BankRef, budget time.Duration) (done bool, err error)
}

// Linker is the part of retain.Linker the maintainer consumes to top up links; retain.Linker satisfies
// it.
type Linker interface {
	// Link adds temporal + semantic (+ causal) edges for units within the per-unit caps, in tx.
	Link(ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes map[port.UnitID][]port.UnitID) error
}

// DefaultGraphBudget is graph_maintenance's time budget per run (SPEC "Jobs": 240 s).
const DefaultGraphBudget = 240 * time.Second
