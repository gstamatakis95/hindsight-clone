package curate

import (
	"context"
	"memd/internal/port"
)

// Rederiver is the rederive_memory job: re-embed, re-resolve, re-link, enqueue consolidation.
type Rederiver interface {
	// Rederive re-embeds the job's unit, re-resolves its entities (or takes names literally), rebuilds
	// its temporal and semantic links and enqueues consolidation.
	Rederive(ctx context.Context, j port.Job) error
}
