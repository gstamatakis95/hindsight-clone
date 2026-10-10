package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// Includer attaches chunks, source facts and entity state within their own token budgets.
type Includer interface {
	// Enrich reads what inc asks for about results; each kind stays within its own budget.
	Enrich(ctx context.Context, b port.BankRef, results []Candidate, inc domain.RecallInclude) (Enrichment, error)
}

// Enrichment is what an Includer attached to a result set.
type Enrichment struct {
	// SourceFacts are the facts behind returned observations, by id.
	SourceFacts map[port.UnitID]domain.Unit
	// SourceFactsTruncated reports that the source-fact budget cut some off.
	SourceFactsTruncated bool
	// Chunks are the source chunks of returned memories, by id.
	Chunks map[port.ChunkID]Chunk
	// Entities is the entity state of returned memories, by id.
	Entities map[port.EntityID]domain.EntityState
}

// Chunk mirrors memory.v1.Chunk: a source chunk attached to recall results.
type Chunk struct {
	// ID is the chunk id.
	ID port.ChunkID
	// Text is the chunk text, possibly cut to fit the chunk budget.
	Text string
	// Index is the chunk's position in its document, from 0.
	Index int
	// Truncated reports that Text was cut to fit the budget.
	Truncated bool
}
