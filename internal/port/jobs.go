package port

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"time"
)

// ---------- jobs (Postgres queue, no broker) ----------

// JobKind is the kind of a job: an alias of domain.OperationKind (use the domain.OperationKind*
// constants). Its string is the operations.kind column value.
type JobKind = domain.OperationKind

// DedupePolicy says which existing row an Enqueue joins instead of inserting a new one. Matching is on
// (bank, kind, serial key) among rows without overrides.
type DedupePolicy int

// Dedupe policies.
const (
	// DedupeNone always inserts a new row.
	DedupeNone DedupePolicy = iota
	// DedupePending joins a PENDING row with the same (bank, kind, serial key), if any.
	DedupePending
	// DedupePendingOrRunning joins a PENDING or PROCESSING row with the same (bank, kind, serial key).
	DedupePendingOrRunning
)

// String names the policy for logs and test output.
func (d DedupePolicy) String() string {
	switch d {
	case DedupeNone:
		return "none"
	case DedupePending:
		return "pending"
	case DedupePendingOrRunning:
		return "pending_or_running"
	default:
		return "unknown"
	}
}

// Job is one row of the operations table: the operation id is the job id.
type Job struct {
	// ID is the operation id; empty on Enqueue (the queue assigns one).
	ID OperationID
	// Bank owns the job.
	Bank BankRef
	// Kind is the job kind.
	Kind JobKind
	// SerialKey allows at most one PROCESSING job per (bank, serial key); empty = no key.
	SerialKey string
	// Payload is kind-specific: document id, unit id, model id, scopes, flags.
	Payload json.RawMessage
	// Overrides are request overrides; nil = none. Jobs with overrides are never deduplicated, in either
	// direction: they neither join a queued row nor are joined by a later Enqueue.
	Overrides *domain.BankConfig
	// ParentID is the retain_batch parent; empty = none.
	ParentID OperationID
	// RunAt is when the job becomes due (debounce, interval floor, backoff); zero = now.
	RunAt time.Time
	// Dedupe is None, Pending or PendingOrRunning, on (bank, kind, serial key). Ignored with Overrides.
	Dedupe DedupePolicy
	// Attempt counts earlier attempts that failed with a retry or were reclaimed; 0 on the first run.
	Attempt int
}

// Queue is the producer side; it writes inside the caller's transaction.
type Queue interface {
	// Enqueue inserts j, or returns the matching row (joined=true) when j.Dedupe finds one.
	// A joined row is left unchanged (use Upgrade to merge flags into it).
	Enqueue(ctx context.Context, j Job) (id OperationID, joined bool, err error)
	// Upgrade merges flags into a pending job (refresh: force_full OR-ed, overrides replaced). The caller
	// computes the merged payload: a non-nil payload replaces the row's payload, and o replaces its
	// overrides (nil clears them). A row that is no longer pending returns domain.ErrFailedPrecondition;
	// an unknown id returns domain.ErrNotFound.
	Upgrade(ctx context.Context, id OperationID, payload json.RawMessage, o *domain.BankConfig) error
	// Reschedule releases or parks a job: it sets run_at = at on a pending row, or returns a processing
	// row to pending with run_at = at (attempt unchanged). A terminal row returns
	// domain.ErrFailedPrecondition; a cancelled row is never overwritten.
	Reschedule(ctx context.Context, id OperationID, at time.Time) error
	// CancelBank cancels every pending job of b and returns how many it cancelled.
	CancelBank(ctx context.Context, b BankRef) (int, error)
}

// Claimer is the worker side; each call is its own short transaction.
// No method ever overwrites a CANCELLED row: completing or failing a cancelled job is a silent no-op.
type Claimer interface {
	// Claim takes up to n due jobs of kinds with FOR UPDATE SKIP LOCKED, skipping a job whose
	// serial key has a PROCESSING row or an older PENDING row, and skipping deleting banks
	// (except their delete_bank job). Claimed rows become PROCESSING with worker_id = worker and a
	// fresh heartbeat; jobs come back oldest first.
	Claim(ctx context.Context, worker string, kinds []JobKind, n int) ([]Job, error)
	// Heartbeat refreshes heartbeat_at on the given PROCESSING jobs held by worker.
	Heartbeat(ctx context.Context, worker string, ids []OperationID) error
	// Progress records the job's latest checkpoint.
	Progress(ctx context.Context, id OperationID, p domain.Progress) error
	// Alive reports whether the job should keep running: false once cancelled or bank deleted
	// (tombstoned or gone; a delete_bank job stays alive), or when the row is gone or terminal.
	Alive(ctx context.Context, id OperationID) (bool, error)
	// Complete marks the job COMPLETED with its result and per-slot usage.
	Complete(ctx context.Context, id OperationID, result json.RawMessage, usage []Usage) error
	// Fail records cause on the job. retryAt != nil returns it to PENDING with run_at = *retryAt and
	// attempt+1; nil = terminal FAILED.
	Fail(ctx context.Context, id OperationID, cause error, retryAt *time.Time) error
	// ReclaimStale returns PROCESSING jobs whose heartbeat is older than ttl to PENDING
	// (attempt+1), or fails them past their kind's max attempts (domain.OperationKind.MaxAttempts:
	// FAILED when attempt+1 >= max, never for an unbounded kind). It returns how many rows it touched.
	ReclaimStale(ctx context.Context, ttl time.Duration) (int, error)
}
