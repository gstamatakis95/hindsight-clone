package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// TagVocabulary lists a bank's tags, for fuzzy tag leaves. CCR WP-09-1 proposes this exact method on
// port.Reader, so a port.Reader satisfies it once the CCR lands.
type TagVocabulary interface {
	// BankTags returns the distinct tags of the bank's live units, most used first (ties by name).
	BankTags(ctx context.Context, b port.BankRef) ([]string, error)
}

// ChunkReader reads source chunks, for include.chunks (CCR WP-09-1, proposed on port.Reader).
type ChunkReader interface {
	// GetChunks returns the bank's chunks with the given ids, in the order of ids; unknown ids are skipped.
	GetChunks(ctx context.Context, b port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error)
}

// EntityStateReader reads entity state, for include.entities (CCR WP-09-1, proposed on port.Reader).
type EntityStateReader interface {
	// EntityStates returns every entity attached to any of units, each with the ids of the live observations
	// attached to it, ordered by entity id.
	EntityStates(ctx context.Context, b port.BankRef, units []port.UnitID) ([]domain.EntityState, error)
}

// Lookups are the reads recall needs beyond port.Reader (CCR WP-09-1). They are optional: without them a
// fuzzy tag leaf fails with domain.ErrFailedPrecondition and include.chunks / include.entities attach
// nothing.
type Lookups interface {
	TagVocabulary
	ChunkReader
	EntityStateReader
}
