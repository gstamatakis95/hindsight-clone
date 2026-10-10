package domain

import (
	"encoding/json"
	"time"
)

// OperationKind is the kind of an operation, which is also its job kind.
type OperationKind string

// Operation kinds, mirroring memory.v1.OperationKind. The string is the operations.kind column value.
const (
	// OperationKindUnspecified is the zero value.
	OperationKindUnspecified OperationKind = ""
	// OperationKindRetainBatch is a parent of retain_document children; it does no work itself.
	OperationKindRetainBatch OperationKind = "retain_batch"
	// OperationKindRetainDocument extracts, embeds, resolves and links one document.
	OperationKindRetainDocument OperationKind = "retain_document"
	// OperationKindConsolidate turns unconsolidated facts into observations.
	OperationKindConsolidate OperationKind = "consolidate"
	// OperationKindRederiveMemory re-embeds, re-resolves and re-links an edited or restored unit.
	OperationKindRederiveMemory OperationKind = "rederive_memory"
	// OperationKindRetagDocument rewrites a document's unit tags and re-consolidates.
	OperationKindRetagDocument OperationKind = "retag_document"
	// OperationKindRefreshMentalModel refreshes one mental model.
	OperationKindRefreshMentalModel OperationKind = "refresh_mental_model"
	// OperationKindGraphMaintenance drains graph_dirty and entity_maintenance.
	OperationKindGraphMaintenance OperationKind = "graph_maintenance"
	// OperationKindDeleteBank deletes a tombstoned bank in batches.
	OperationKindDeleteBank OperationKind = "delete_bank"
)

// DefaultMaxAttempts is the attempt cap of every retried kind except consolidate.
const DefaultMaxAttempts = 3

// MaxAttempts is the number of attempts after which a failing or reclaimed job of kind k fails for good.
// Zero means unbounded: consolidate retries indefinitely with capped backoff.
func (k OperationKind) MaxAttempts() int {
	if k == OperationKindConsolidate {
		return 0
	}
	return DefaultMaxAttempts
}

// HasFinalizer reports whether a job of kind k that exhausts its attempts through reclaim is handed back
// to its handler's finalizer instead of being failed by the Claimer. Only refresh_mental_model has one:
// its final failure must also pause the model and write REFRESH_FAILED history (decision 012, R10).
func (k OperationKind) HasFinalizer() bool {
	return k == OperationKindRefreshMentalModel
}

// OperationStatus is an operation's lifecycle state.
type OperationStatus string

// Operation statuses, mirroring memory.v1.OperationStatus. The string is the operations.status value.
const (
	// OperationStatusUnspecified is the zero value.
	OperationStatusUnspecified OperationStatus = ""
	// OperationStatusPending is queued (possibly parked until run_at).
	OperationStatusPending OperationStatus = "pending"
	// OperationStatusProcessing is claimed by a worker.
	OperationStatusProcessing OperationStatus = "processing"
	// OperationStatusCompleted finished successfully.
	OperationStatusCompleted OperationStatus = "completed"
	// OperationStatusFailed failed for good (RetryOperation re-queues it).
	OperationStatusFailed OperationStatus = "failed"
	// OperationStatusCancelled was cancelled (RetryOperation re-queues it).
	OperationStatusCancelled OperationStatus = "cancelled"
)

// Terminal reports whether the status is final (completed, failed or cancelled).
func (s OperationStatus) Terminal() bool {
	return s == OperationStatusCompleted || s == OperationStatusFailed || s == OperationStatusCancelled
}

// Progress is a handler's latest checkpoint.
type Progress struct {
	// Stage names the current step.
	Stage string `json:"stage"`
	// At is when the progress was reported.
	At time.Time `json:"at"`
	// Processed counts items done.
	Processed int64 `json:"processed"`
	// Total counts items overall, when known.
	Total int64 `json:"total"`
}

// Operation is one row of the operations table as clients see it.
type Operation struct {
	// ID is the operation (and job) id.
	ID OperationID
	// Bank owns the operation.
	Bank BankRef
	// Kind is the job kind.
	Kind OperationKind
	// Status is the lifecycle state.
	Status OperationStatus
	// SerialKey serializes jobs per bank (empty = none).
	SerialKey string
	// Error is the last error.
	Error string
	// Result is a kind-specific summary, informational only.
	Result json.RawMessage
	// Usage is per-slot token usage.
	Usage []Usage
	// EffectiveOverrides are the overrides the run used.
	EffectiveOverrides *BankConfig
	// ParentID is the retain_batch parent, if any.
	ParentID OperationID
	// Progress is the latest checkpoint.
	Progress *Progress
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
	// NextRunAt is when a parked job becomes due (backoff, debounce or interval floor).
	NextRunAt *time.Time
	// Attempts is the number of attempts so far.
	Attempts int
	// HeartbeatAt is the worker's last heartbeat while processing.
	HeartbeatAt *time.Time
	// WorkerID is the worker holding the job while processing.
	WorkerID string
}

// EventType is the type of a lifecycle event.
type EventType string

// Event types, mirroring memory.v1.EventType.
const (
	// EventTypeUnspecified is the zero value.
	EventTypeUnspecified EventType = ""
	// EventTypeRetainCompleted is written when a document's retain completes.
	EventTypeRetainCompleted EventType = "retain_completed"
	// EventTypeConsolidationCompleted is written when a consolidation run completes.
	EventTypeConsolidationCompleted EventType = "consolidation_completed"
	// EventTypeMentalModelRefreshed is written when a refresh writes a version.
	EventTypeMentalModelRefreshed EventType = "mental_model_refreshed"
	// EventTypeMentalModelRefreshFailed is written when a refresh fails.
	EventTypeMentalModelRefreshFailed EventType = "mental_model_refresh_failed"
	// EventTypeMemoryCurated is written by edit, invalidate and restore.
	EventTypeMemoryCurated EventType = "memory_curated"
	// EventTypeOperationFailed is written when an operation fails for good.
	EventTypeOperationFailed EventType = "operation_failed"
)

// LifecycleEvent is one row of lifecycle_events, written in the state change's transaction.
type LifecycleEvent struct {
	// Seq is the bank change_seq of the writing transaction; events of one tx share it.
	Seq int64
	// Type is the event type.
	Type EventType
	// Bank owns the event.
	Bank BankRef
	// At is when the event was written.
	At time.Time
	// OperationID is the operation that caused it, if any.
	OperationID OperationID
	// SubjectID is the document, memory or mental model id.
	SubjectID string
}
