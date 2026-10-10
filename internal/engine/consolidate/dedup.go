package consolidate

import (
	"context"
	"memd/internal/engine/recall"
	"memd/internal/port"
)

// Deduper merges near-duplicate observations within a scope.
type Deduper interface {
	// Reconcile finds observations with cosine >= dedup_threshold within scope s of each changed one,
	// then makes an LLM merge-or-keep check (DEDUP_CHECK). It returns the observations it removed by
	// merging.
	Reconcile(ctx context.Context, tx port.Tx, s port.ScopeKey, changed []port.UnitID) ([]port.UnitID, error)
}

// Recaller is the part of recall.Recaller consolidation consumes (near-duplicate and related-observation
// lookups); recall.Recaller satisfies it.
type Recaller interface {
	// Recall runs one recall request.
	Recall(ctx context.Context, req recall.RecallRequest) (recall.RecallResult, error)
}
