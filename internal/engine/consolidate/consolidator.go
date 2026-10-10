package consolidate

import (
	"context"
	"memd/internal/port"
)

// Consolidator is the consolidate job handler.
type Consolidator interface {
	// Run takes up to consolidation_max_units_per_run of the bank's oldest unconsolidated units (only
	// the job's scopes, for a manual run). Each unit is decided in every scope it belongs to, then stamped
	// in the same tx as its last scope's writes; a scope that already cites the unit is skipped, so a
	// replayed run is safe. A failing LLM batch is halved and retried; a unit that fails alone is parked
	// with consolidation_failed_at. More=true means re-enqueue.
	Run(ctx context.Context, j port.Job) (ConsolidationResult, error)
}

// ConsolidationResult summarizes one consolidation run.
type ConsolidationResult struct {
	// Processed counts the units decided (stamped or parked) in this run.
	Processed int
	// Changed are the scopes whose observations changed; drives mental-model refresh triggers.
	Changed []port.ScopeKey
	// More reports that unconsolidated units remain, so the handler re-enqueues (DedupePending).
	More bool
}
