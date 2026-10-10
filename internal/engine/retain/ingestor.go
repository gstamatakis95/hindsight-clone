package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Ingestor is the api-side half of Retain.
type Ingestor interface {
	// Ingest validates (scope fan-out, overrides), stores blobs, then runs inline or enqueues. An
	// ALL_COMBINATIONS fan-out over max_combinations fails with domain.ErrTooManyScopes before anything
	// is stored.
	Ingest(ctx context.Context, req IngestRequest) (IngestResult, error)
}

// IngestRequest mirrors memory.v1.RetainRequest with domain types.
type IngestRequest struct {
	// Bank receives the items; an unknown bank is created with tenant defaults.
	Bank port.BankRef
	// Items are the documents to retain.
	Items []IngestItem
	// DocumentTags are added to every item's tags.
	DocumentTags []string
	// Overrides apply to every item, below each item's own overrides; nil = none.
	Overrides *domain.BankConfig
	// SkipConsolidation suppresses the automatic consolidate job after retain.
	SkipConsolidation bool
	// RunAsync enqueues retain_document jobs and returns operation ids instead of waiting for units.
	RunAsync bool
}

// IngestItem mirrors memory.v1.RetainItem: one document to retain.
type IngestItem struct {
	// Content is the original text.
	Content string
	// Context is free-form context stored on every unit (speaker, conversation, source).
	Context string
	// Timestamp is when the content was said; nil = now (from port.Clock).
	Timestamp *time.Time
	// Timeless marks content with no time anchor; it wins over Timestamp.
	Timeless bool
	// DocumentID is the upsert key (replace); empty = generated.
	DocumentID port.DocumentID
	// Tags are the item's tags (merged with IngestRequest.DocumentTags).
	Tags []string
	// ObservationScopes picks the item's scope fan-out; nil = the bank's default.
	ObservationScopes *domain.ObservationScopes
	// Metadata is string metadata stored on every unit.
	Metadata map[string]string
	// Entities are caller-supplied entity hints merged with extracted mentions.
	Entities []domain.EntityHint
	// ResolveEntities false takes entity names literally; nil = true.
	ResolveEntities *bool
	// Overrides are item-level overrides, the highest level; nil = none.
	Overrides *domain.BankConfig
	// ForceReextract re-runs extraction even when content and overrides are unchanged.
	ForceReextract bool
}

// IngestResult mirrors memory.v1.RetainResponse.
type IngestResult struct {
	// Documents has one entry per item, in item order: sync = filled; async = operation ids only.
	Documents []RetainedDocument
	// ParentOperationID is the retain_batch parent, set when there is more than one document.
	ParentOperationID port.OperationID
}

// RetainedDocument mirrors memory.v1.RetainedDocument: the outcome for one item.
type RetainedDocument struct {
	// DocumentID is the item's document (generated when the item left it empty).
	DocumentID port.DocumentID
	// UnitsCreated counts the units inserted (sync only).
	UnitsCreated int
	// Unchanged reports the same content + overrides hash; nothing re-extracted.
	Unchanged bool
	// OperationID is the retain_document operation (async), or empty for an inline run.
	OperationID port.OperationID
}
