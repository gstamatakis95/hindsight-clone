package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// EntityResolver maps a fact's free-form entity mentions to existing or new entities.
type EntityResolver interface {
	// Resolve scores candidates by name similarity + co-occurrence overlap + temporal proximity and
	// merges a mention into the best candidate at or above the threshold, else creates an entity. It
	// runs inside the chunk batch's transaction and makes no model call.
	Resolve(ctx context.Context, tx port.Tx, b port.BankRef, in ResolveInput) ([]port.EntityID, error)
}

// ResolveInput is the entity mentions of one fact to resolve.
type ResolveInput struct {
	// Mentions are the fact's extracted mentions.
	Mentions []Mention
	// Hints are the retain item's caller-supplied entities, merged with Mentions (deduplicated by name).
	Hints []domain.EntityHint
	// Literal takes names as given with no fuzzy matching (resolve_entities=false).
	Literal bool
	// At is the fact's occurrence or mention time, used for temporal proximity; zero = unknown.
	At time.Time
	// Threshold is the merge threshold (RetainTuning.EntityMatchThreshold).
	Threshold float64
}
