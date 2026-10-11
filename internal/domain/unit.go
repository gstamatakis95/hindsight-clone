package domain

import (
	"encoding/json"
	"time"
)

// FactType classifies a memory unit.
type FactType string

// Fact types, mirroring memory.v1.FactType.
const (
	// FactTypeUnspecified is the zero value.
	FactTypeUnspecified FactType = ""
	// FactTypeWorld is a fact about the world.
	FactTypeWorld FactType = "world"
	// FactTypeExperience is something the agent did or experienced.
	FactTypeExperience FactType = "experience"
	// FactTypeObservation is a consolidated observation derived from facts.
	FactTypeObservation FactType = "observation"
)

// MemoryState says whether a unit is live or archived by invalidate.
type MemoryState string

// Memory states, mirroring memory.v1.MemoryState.
const (
	// MemoryStateUnspecified is the zero value: ListMemories applies no state filter (both tables);
	// UpdateMemory leaves the state unchanged.
	MemoryStateUnspecified MemoryState = ""
	// MemoryStateValid is a live unit in memory_units.
	MemoryStateValid MemoryState = "valid"
	// MemoryStateInvalidated is an archived unit in memory_units_archive.
	MemoryStateInvalidated MemoryState = "invalidated"
)

// Unit is one memory unit: a world or experience fact, or an observation.
type Unit struct {
	// ID is the unit id.
	ID UnitID
	// Bank owns the unit.
	Bank BankRef
	// DocumentID is the source document; empty for observations.
	DocumentID DocumentID
	// ChunkID is the source chunk; empty for observations and edited units without one.
	ChunkID ChunkID
	// Type is the fact type.
	Type FactType
	// Text is the fact or observation text.
	Text string
	// Context is free-form context from the retain item.
	Context string
	// Metadata is string metadata from the retain item.
	Metadata map[string]string
	// Tags are normalized tags; for an observation, its scope's tags.
	Tags []string
	// Entities are canonical entity names attached to the unit (filled by readers).
	Entities []string
	// Embedding is the 768-dim vector; nil after an edit until rederive re-embeds.
	Embedding []float32
	// OccurredStart is when the fact started; nil = unknown or timeless.
	OccurredStart *time.Time
	// OccurredEnd is when the fact ended; nil = unknown or timeless.
	OccurredEnd *time.Time
	// MentionedAt is when the fact was said (the item timestamp); nil when timeless.
	MentionedAt *time.Time
	// Timeless marks content with no time anchor; it wins over a timestamp.
	Timeless bool
	// ProofCount is how many facts support an observation.
	ProofCount int
	// SourceIDs are the facts an observation cites (filled by readers).
	SourceIDs []UnitID
	// Version is an observation's version, bumped on every update.
	Version int64
	// Seq is the bank change sequence of the transaction that last wrote the unit.
	Seq int64
	// ConsolidatedAt is set once every scope of the unit has been decided.
	ConsolidatedAt *time.Time
	// ConsolidationFailedAt parks a unit that failed consolidation alone.
	ConsolidationFailedAt *time.Time
	// EditedAt is set by a curation edit.
	EditedAt *time.Time
	// Stale is set on an observation by Reader.GetUnit and Reader.ListUnits when a live fact in its scope
	// waits for consolidation; it is computed at read time (never stored) and always false elsewhere.
	Stale bool
	// State is valid for live units and invalidated for archived ones.
	State MemoryState
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// Scope returns the observation scope of an observation unit (its bank and tags).
func (u Unit) Scope() ScopeKey { return NewScopeKey(u.Bank, u.Tags) }

// Chunk is one stored piece of a document, with its own content hash so a retain can resume.
type Chunk struct {
	// ID is the chunk id.
	ID ChunkID
	// Bank owns the chunk.
	Bank BankRef
	// DocumentID is the parent document.
	DocumentID DocumentID
	// Index is the chunk's position in the document, from 0.
	Index int
	// Text is the chunk text.
	Text string
	// ContentHash is the hash of Text.
	ContentHash string
	// TokenCount is the chunk's token count.
	TokenCount int
	// CreatedAt is the creation time.
	CreatedAt time.Time
}

// Document is one retained document and whether its last retain finished.
type Document struct {
	// ID is the document id.
	ID DocumentID
	// Bank owns the document.
	Bank BankRef
	// ContentHash is the hash of the original text of the last completed retain.
	ContentHash string
	// OverridesHash is the hash of the stored overrides of the last completed retain.
	OverridesHash string
	// Overrides are the retain overrides replayed by ReprocessDocument; nil = none.
	Overrides *BankConfig
	// Tags are the document's normalized tags.
	Tags []string
	// BlobKey is where the original text lives.
	BlobKey string
	// ObservationScopes is the retain item's observation_scopes, kept across reprocess; nil = none (the
	// stored overrides' or the bank's default applies).
	ObservationScopes *ObservationScopes
	// ExtractedAt is set when the last retain committed its last chunk; nil while in flight or failed.
	ExtractedAt *time.Time
	// UnitCount is the number of live units (filled by readers).
	UnitCount int64
	// UnitsByFactType counts live units per fact type (filled by readers).
	UnitsByFactType map[FactType]int64
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// TimeWindow is a half-open time range [Start, End). A zero bound is unbounded.
type TimeWindow struct {
	// Start is the inclusive start; zero = unbounded.
	Start time.Time
	// End is the exclusive end; zero = unbounded.
	End time.Time
}

// MinScores are recall floors: arm floors apply in retrieval, the rest after scoring.
type MinScores struct {
	// Arms are the retrieval floors by arm name; each arm reads its own entry (a missing entry = the arm's
	// default).
	Arms map[string]float64
	// Reranker is the cross-encoder score floor.
	Reranker *float64
	// Final is the final score floor.
	Final *float64
}

// RecallInclude asks recall to attach chunks, source facts and entity state within their own budgets.
type RecallInclude struct {
	// Chunks attaches source chunks.
	Chunks *bool
	// ChunksMaxTokens is the chunk budget.
	ChunksMaxTokens *int
	// SourceFacts attaches the facts behind returned observations.
	SourceFacts *bool
	// SourceFactsMaxTokens is the source-fact budget.
	SourceFactsMaxTokens *int
	// Entities attaches entity state.
	Entities *bool
}

// TimeField selects the timestamp ListMemories filters on.
type TimeField string

// Memory time fields, mirroring memory.v1.TimeField.
const (
	// TimeFieldUnspecified is the zero value (created_at).
	TimeFieldUnspecified TimeField = ""
	// TimeFieldCreatedAt filters on created_at.
	TimeFieldCreatedAt TimeField = "created_at"
	// TimeFieldUpdatedAt filters on updated_at.
	TimeFieldUpdatedAt TimeField = "updated_at"
	// TimeFieldMentionedAt filters on mentioned_at.
	TimeFieldMentionedAt TimeField = "mentioned_at"
	// TimeFieldOccurredStart filters on occurred_start.
	TimeFieldOccurredStart TimeField = "occurred_start"
	// TimeFieldOccurredEnd filters on occurred_end.
	TimeFieldOccurredEnd TimeField = "occurred_end"
)

// DocTimeField selects the timestamp ListDocuments filters on.
type DocTimeField string

// Document time fields, mirroring memory.v1.DocTimeField.
const (
	// DocTimeFieldUnspecified is the zero value (created_at).
	DocTimeFieldUnspecified DocTimeField = ""
	// DocTimeFieldCreatedAt filters on created_at.
	DocTimeFieldCreatedAt DocTimeField = "created_at"
	// DocTimeFieldUpdatedAt filters on updated_at.
	DocTimeFieldUpdatedAt DocTimeField = "updated_at"
)

// ObservationVersion is one entry of an observation's history, written on every create and update.
type ObservationVersion struct {
	// Version is the observation version.
	Version int64
	// Text is the observation text at that version.
	Text string
	// SourceIDs are the facts cited at that version.
	SourceIDs []UnitID
	// At is when the version was written.
	At time.Time
}

// CurationAction names what a curation audit row records.
type CurationAction string

// Curation actions.
const (
	// CurationEdit is an UpdateMemory edit.
	CurationEdit CurationAction = "edit"
	// CurationInvalidate archives a unit.
	CurationInvalidate CurationAction = "invalidate"
	// CurationRestore moves an archived unit back.
	CurationRestore CurationAction = "restore"
)

// AuditEntry is one memory_audit row: what a curation action changed and why.
type AuditEntry struct {
	// Bank owns the unit.
	Bank BankRef
	// UnitID is the curated unit.
	UnitID UnitID
	// Action is edit, invalidate or restore.
	Action CurationAction
	// Reason is the caller's reason.
	Reason string
	// Before is a JSON snapshot of the changed fields before the action; nil = none.
	Before json.RawMessage
	// After is a JSON snapshot of the changed fields after the action; nil = none.
	After json.RawMessage
	// OperationID is the rederive operation, if any.
	OperationID OperationID
	// At is when the action committed.
	At time.Time
}

// Evidence ties an observation to one fact it rests on, with the exact supporting quote.
type Evidence struct {
	// Unit is the cited fact.
	Unit UnitID
	// Quote is the exact text of the fact that supports the observation.
	Quote string
}
