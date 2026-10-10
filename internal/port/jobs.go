package port

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"time"
)

// ---------- jobs (Postgres queue, no broker) ----------

// JobKind is the kind of a job: an alias of domain.OperationKind (use the domain.OperationKind*
// constants). Its string is the operations.kind column value.
type JobKind = domain.OperationKind

// DedupePolicy says which existing row an Enqueue joins instead of inserting a new one. Matching is on
// (bank, kind, serial key) among joinable rows (see Job.Overrides and decision 012).
type DedupePolicy int

// Dedupe policies.
const (
	// DedupeNone always inserts a new row, and that row is never joined by a later Enqueue.
	DedupeNone DedupePolicy = iota
	// DedupePending joins a joinable PENDING row (due or parked) with the same (bank, kind, serial key).
	DedupePending
	// DedupePendingOrRunning joins a joinable PENDING or PROCESSING row with the same (bank, kind, serial key).
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
	// SerialKey allows at most one running job per (bank, serial key); empty = no key (stored as NULL).
	SerialKey string
	// Payload is a kind-specific JSON object: document id, unit id, model id, scopes, flags. Optional
	// fields are omitted when unset so Upgrade can tell "absent" from "set".
	Payload json.RawMessage
	// Overrides are request overrides; nil = none. A job with overrides is never deduplicated in either
	// direction (it neither joins a queued row nor is joined later), except refresh_mental_model, which
	// always joins its model's queued refresh and is then upgraded (decision 012, R1).
	Overrides *domain.BankConfig
	// ParentID is the retain_batch parent; empty = none.
	ParentID OperationID
	// RunAt is when the job becomes due (debounce, interval floor, backoff); zero = now.
	RunAt time.Time
	// Dedupe is None, Pending or PendingOrRunning, on (bank, kind, serial key).
	Dedupe DedupePolicy
	// Attempt counts earlier attempts that failed with a retry or were reclaimed; 0 on the first run.
	// A job claimed with Attempt >= Kind.MaxAttempts() (only kinds with HasFinalizer) is due its finalizer.
	Attempt int
}

// OperationFilter selects operations for ListOperations; zero fields do not filter.
type OperationFilter struct {
	// Status keeps only operations in this status.
	Status domain.OperationStatus
	// Kind keeps only operations of this kind.
	Kind JobKind
	// ExcludeParents drops retain_batch parents.
	ExcludeParents bool
	// PageSize caps the page (the adapter applies a default and a maximum).
	PageSize int
	// PageToken continues a previous page; empty = first page.
	PageToken string
}

// LostJobsError reports jobs a worker no longer holds: reclaimed, cancelled, finished or re-claimed by
// another worker. Its Unwrap is domain.ErrConflict, so errors.Is(err, domain.ErrConflict) holds.
type LostJobsError struct {
	// IDs are the jobs the worker lost.
	IDs []OperationID
}

// Error lists the lost job ids.
func (e *LostJobsError) Error() string {
	return fmt.Sprintf("%v: worker no longer holds jobs %v", domain.ErrConflict, e.IDs)
}

// Unwrap returns domain.ErrConflict.
func (e *LostJobsError) Unwrap() error { return domain.ErrConflict }

// LostJobs returns the job ids carried by err, if err is (or wraps) a *LostJobsError.
func LostJobs(err error) []OperationID {
	var lost *LostJobsError
	if errors.As(err, &lost) {
		return lost.IDs
	}
	return nil
}

// Queue is the operations table seen from a transaction: the producer side plus operation control.
// Everything it writes commits (or rolls back) with the caller's transaction.
type Queue interface {
	// Enqueue inserts j, or returns the matching joinable row (joined=true, row left unchanged) when
	// j.Dedupe finds one. The adapter serializes dedupe Enqueues per (tenant, bank, kind, serial key) for
	// the rest of the transaction (Postgres: pg_advisory_xact_lock), so concurrent enqueuers never both
	// insert. A tombstoned bank returns domain.ErrBankDeleting (except for a delete_bank job); an unknown
	// bank returns domain.ErrNotFound.
	Enqueue(ctx context.Context, j Job) (id OperationID, joined bool, err error)
	// Upgrade merges a joiner's request into a PENDING job under the row lock. payload is a JSON object
	// shallow-merged into the row's payload: a key present in payload replaces the row's value, except a
	// boolean, which is OR-ed (a true flag such as force_full is never cleared); absent keys are kept. o
	// non-nil replaces the overrides; nil keeps them. A row that is not PENDING returns
	// domain.ErrFailedPrecondition; an unknown id returns domain.ErrNotFound.
	Upgrade(ctx context.Context, id OperationID, payload json.RawMessage, o *domain.BankConfig) error
	// Reschedule releases or parks a PENDING job by setting run_at = at. Any other status returns
	// domain.ErrFailedPrecondition and changes nothing; an unknown id returns domain.ErrNotFound.
	Reschedule(ctx context.Context, id OperationID, at time.Time) error
	// CancelBank cancels every PENDING job of b except its delete_bank job and returns how many it
	// cancelled. Processing jobs of b see Alive=false once the bank is tombstoned.
	CancelBank(ctx context.Context, b BankRef) (int, error)
	// Get returns one operation of b; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b BankRef, id OperationID) (domain.Operation, error)
	// List pages the bank's operations, newest first, and returns the next page token ("" = last page).
	List(ctx context.Context, b BankRef, f OperationFilter) ([]domain.Operation, string, error)
	// Cancel cancels a PENDING or PROCESSING operation and returns it. A pending row is finished at once.
	// A processing row becomes CANCELLED but keeps holding its serial key until its handler's terminal
	// Complete or Fail (or ReclaimStale, if the worker died) finishes it; committed work stays. Other
	// statuses return domain.ErrFailedPrecondition.
	Cancel(ctx context.Context, b BankRef, id OperationID) (domain.Operation, error)
	// Retry resets a FAILED or finished CANCELLED operation to PENDING with run_at = now and returns it
	// (attempt reset to 0, error and finished_at cleared). Other statuses return domain.ErrFailedPrecondition.
	Retry(ctx context.Context, b BankRef, id OperationID) (domain.Operation, error)
	// Prune deletes terminal, finished operations whose finished_at is before before and returns how many.
	// Pending and processing operations are never pruned.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// Claimer is the worker side; each call is its own short transaction.
//
// Fencing: Progress, Complete and Fail act only on a PROCESSING row; on any other status they change
// nothing and return domain.ErrFailedPrecondition, with one exception: on a row cancelled while
// processing, Complete and Fail finish it (set finished_at, releasing the serial key), keep it CANCELLED
// and return domain.ErrCancelled. An unknown id (for example the delete_bank job whose final transaction
// deleted its own row with the bank) returns domain.ErrNotFound, which the runner treats as done for
// delete_bank. A worker that lost its job (reclaimed and re-claimed elsewhere) learns it from Heartbeat's
// *LostJobsError and must stop the handler.
type Claimer interface {
	// Claim takes up to n due jobs of kinds with FOR UPDATE SKIP LOCKED, skipping a job whose
	// serial key has a PROCESSING row, a cancelled-but-unfinished row or an older due PENDING row, and
	// skipping deleting banks (except their delete_bank job). Claimed rows become PROCESSING with
	// worker_id = worker and a fresh heartbeat; jobs come back oldest first. A lost race on the unique
	// processing index is retried inside Claim and never surfaces as an error.
	Claim(ctx context.Context, worker string, kinds []JobKind, n int) ([]Job, error)
	// Heartbeat refreshes heartbeat_at on the given jobs held by worker: PROCESSING rows and rows cancelled
	// while processing that are not finished yet (so a cancelled job keeps its serial key while its handler
	// winds down). If any id is not PROCESSING under worker (cancelled, reclaimed, finished or re-claimed by
	// another worker) it returns a *LostJobsError naming those ids, after refreshing the held ones; the
	// runner stops their handlers.
	Heartbeat(ctx context.Context, worker string, ids []OperationID) error
	// Progress records the job's latest checkpoint (fenced: PROCESSING rows only).
	Progress(ctx context.Context, id OperationID, p domain.Progress) error
	// Alive reports whether the job should keep running: true only while it is PROCESSING and its bank is
	// neither tombstoned nor gone (a delete_bank job stays alive). An unknown id is (false, nil).
	Alive(ctx context.Context, id OperationID) (bool, error)
	// Complete marks the job COMPLETED (and finished) with its result and per-slot usage (fenced).
	Complete(ctx context.Context, id OperationID, result json.RawMessage, usage []Usage) error
	// Fail records cause on the job (fenced). retryAt != nil returns it to PENDING with run_at = *retryAt
	// and attempt+1; nil = terminal FAILED, which also appends an OPERATION_FAILED lifecycle event under a
	// fresh bank change seq (skipped when the bank is tombstoned or gone).
	Fail(ctx context.Context, id OperationID, cause error, retryAt *time.Time) error
	// ReclaimStale handles PROCESSING jobs whose heartbeat is older than ttl. Below their kind's max
	// attempts (domain.OperationKind.MaxAttempts; 0 = unbounded) they return to PENDING with attempt+1 and
	// run_at unchanged. At attempt+1 >= max they fail like Fail(nil) (FAILED plus OPERATION_FAILED),
	// except kinds with HasFinalizer, which return to PENDING with Attempt = max so the runner hands them
	// to the kind's finalizer. Cancelled-but-unfinished rows with a stale heartbeat are finished. Terminal
	// rows are never touched. It returns how many rows it changed.
	ReclaimStale(ctx context.Context, ttl time.Duration) (int, error)
}
