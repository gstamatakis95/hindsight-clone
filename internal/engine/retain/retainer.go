package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// Retainer processes one document; it is also the retain_document job handler.
type Retainer interface {
	// ProcessDocument runs chunk -> extract -> normalize -> embed -> resolve -> link -> commit, one tx per
	// chunk batch. Unchanged content + overrides hash with extracted_at set = no-op (unless
	// force_reextract). A changed document replaces its old units in the first batch's tx; a retried job
	// skips chunks already committed with a matching hash. Runs inline for sync Retain, else as a job.
	ProcessDocument(ctx context.Context, j port.Job) (RetainResult, error)
}

// RetainResult summarizes one processed document.
type RetainResult struct { //nolint:revive // stutter: name fixed by the spec
	// DocumentID is the processed document.
	DocumentID port.DocumentID
	// UnitsCreated counts the units this run inserted.
	UnitsCreated int
	// UnitIDs are the inserted units, in chunk and extraction order.
	UnitIDs []port.UnitID
	// Unchanged reports a no-op: same content and overrides hash as the last completed retain.
	Unchanged bool
	// ChunksTotal is the number of chunks the document split into.
	ChunksTotal int
	// ChunksSkipped counts chunks already committed with a matching hash (a resumed retain).
	ChunksSkipped int
	// Scopes are the observation scopes the new units consolidate under.
	Scopes []port.ScopeKey
	// Usage is per-slot token usage (EXTRACT, TIME_PARSE, EMBED).
	Usage []domain.Usage
}
