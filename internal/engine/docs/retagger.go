package docs

import (
	"context"
	"memd/internal/port"
)

// Retagger replaces a document's tags and re-consolidates what depended on the old ones.
type Retagger interface {
	// Retag replaces doc's tag set and returns the retag_document operation; a set-equal update is a
	// no-op and returns an empty id.
	Retag(ctx context.Context, b port.BankRef, doc port.DocumentID, tags []string) (port.OperationID, error)
}

// Invalidator is the part of consolidate.Invalidator the retagger consumes; consolidate.Invalidator
// satisfies it.
type Invalidator interface {
	// ClearDocumentUnits clears the observations citing any unit of doc, resets their co-sources and
	// returns the affected scopes. It runs in the caller's transaction.
	ClearDocumentUnits(ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID) ([]port.ScopeKey, error)
}
