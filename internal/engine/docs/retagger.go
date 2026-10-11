package docs

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
)

// Retagger replaces a document's tags and re-consolidates what depended on the old ones.
type Retagger interface {
	// Retag replaces doc's tag set and returns the retag_document operation; a set-equal update is a
	// no-op and returns an empty id. The new tags are committed on the document row in the same
	// transaction as the enqueue (DedupePending), so a retag that joins a pending one is never lost.
	Retag(ctx context.Context, b domain.BankRef, doc domain.DocumentID, tags []string) (domain.OperationID, error)
	// Apply is the retag_document job: it re-reads the document's current tags, rewrites the tags of the
	// document's live units, deletes the observations citing them, resets their co-sources and enqueues
	// consolidation. A document that is gone is a no-op; running it twice is harmless.
	Apply(ctx context.Context, j store.Job) error
}
