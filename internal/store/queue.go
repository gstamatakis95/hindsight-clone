package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"time"
)

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
	ID domain.OperationID
	// Bank owns the job.
	Bank domain.BankRef
	// Kind is the job kind.
	Kind domain.OperationKind
	// SerialKey allows at most one running job per (bank, serial key); empty = no key (stored as NULL).
	SerialKey string
	// Payload is a kind-specific JSON object: document id, unit id, model id, scopes, flags. Optional
	// fields are omitted when unset so Upgrade can tell "absent" from "set".
	Payload json.RawMessage
	// Overrides are request overrides; nil = none. A job with overrides is never deduplicated in either
	// direction (it neither joins a queued row nor is joined later), except refresh_mental_model, which
	// always joins its model's queued refresh and is then upgraded (decision 012, R1).
	// Self-continuations (consolidate, graph_maintenance, delete_bank re-enqueuing leftover work) use
	// DedupePending.
	Overrides *domain.BankConfig
	// ParentID is the retain_batch parent; empty = none.
	ParentID domain.OperationID
	// RunAt is when the job becomes due (debounce, interval floor, backoff); zero = now.
	RunAt time.Time
	// Dedupe is None, Pending or PendingOrRunning, on (bank, kind, serial key).
	Dedupe DedupePolicy
	// Attempt counts earlier attempts that failed with a retry or were reclaimed; 0 on the first run.
	Attempt int
}

// OperationFilter selects operations for ListOperations; zero fields do not filter.
type OperationFilter struct {
	// Status keeps only operations in this status.
	Status domain.OperationStatus
	// Kind keeps only operations of this kind.
	Kind domain.OperationKind
	// ExcludeParents drops retain_batch parents.
	ExcludeParents bool
	// PageSize caps the page (the adapter applies a default and a maximum).
	PageSize int
	// PageToken continues a previous page; empty = first page.
	PageToken string
}

// LostJobsError reports jobs whose handler must stop: cancel requested, reclaimed, finished or re-claimed
// by another worker. Its Unwrap is domain.ErrConflict, so errors.Is(err, domain.ErrConflict) holds.
type LostJobsError struct {
	// IDs are the jobs the worker lost.
	IDs []domain.OperationID
}

// Error lists the lost job ids.
func (e *LostJobsError) Error() string {
	return fmt.Sprintf("%v: worker no longer holds jobs %v", domain.ErrConflict, e.IDs)
}

// Unwrap returns domain.ErrConflict.
func (e *LostJobsError) Unwrap() error { return domain.ErrConflict }

// LostJobs returns the job ids carried by err, if err is (or wraps) a *LostJobsError.
func LostJobs(err error) []domain.OperationID {
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
	// j.Dedupe finds one. Rows enqueued with DedupeNone or with overrides, and rows whose cancel was
	// requested, are never joined. A refresh_mental_model job always dedupes as DedupePending (DedupeNone
	// is treated as Pending). The adapter serializes dedupe Enqueues per (tenant, bank, kind, serial key)
	// for the rest of the transaction (Postgres: pg_advisory_xact_lock), so concurrent enqueuers never both
	// insert. A tombstoned bank returns domain.ErrBankDeleting (except for a delete_bank job); an unknown
	// bank returns domain.ErrNotFound.
	// Adapter obligations: take the advisory lock in its own statement before the lookup (keys in sorted
	// order when one tx enqueues several, or retry on 40P01); lock a joined row FOR UPDATE so a claim can't
	// land between the join and an Upgrade; take FOR KEY SHARE on the bank row.
	Enqueue(ctx context.Context, j Job) (id domain.OperationID, joined bool, err error)
	// Upgrade merges a joiner's request into a PENDING job under the row lock. payload is a JSON object
	// shallow-merged into the row's payload: a key present in payload replaces the row's value, except
	// force_full, which is OR-ed (never cleared); absent keys are kept (a nil payload keeps the whole
	// payload). o non-nil replaces the overrides; nil keeps them. A row that is not PENDING returns
	// domain.ErrFailedPrecondition; an unknown id returns domain.ErrNotFound.
	Upgrade(ctx context.Context, id domain.OperationID, payload json.RawMessage, o *domain.BankConfig) error
	// Reschedule releases or parks a PENDING job by setting run_at = at. Any other status returns
	// domain.ErrFailedPrecondition and changes nothing; an unknown id returns domain.ErrNotFound.
	Reschedule(ctx context.Context, id domain.OperationID, at time.Time) error
	// CancelBank cancels every PENDING job of b except its delete_bank job and returns how many it
	// cancelled. Processing jobs of b see Alive=false once the bank is tombstoned.
	CancelBank(ctx context.Context, b domain.BankRef) (int, error)
	// Get returns one operation of b; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error)
	// List pages the bank's operations, newest first, and returns the next page token ("" = last page).
	List(ctx context.Context, b domain.BankRef, f OperationFilter) ([]domain.Operation, string, error)
	// Cancel cancels a PENDING or PROCESSING operation and returns it. A pending row becomes CANCELLED at
	// once. On a processing row it only records the request (cancel_requested_at): the status stays
	// PROCESSING, so the serial key stays held, Alive turns false and Heartbeat reports the job, and the
	// handler's terminal Complete or Fail (or ReclaimStale, if the worker died) moves it to CANCELLED;
	// committed work stays. Other statuses return domain.ErrFailedPrecondition; an unknown id returns
	// domain.ErrNotFound.
	Cancel(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error)
	// Retry resets a FAILED or CANCELLED operation to PENDING with run_at = now and returns it (attempt
	// reset to 0; error, finished_at and the cancel request cleared). Other statuses (including a
	// cancel-requested PROCESSING row) return domain.ErrFailedPrecondition; an unknown id returns
	// domain.ErrNotFound.
	Retry(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error)
	// Prune deletes terminal, finished operations whose finished_at is before before and returns how many.
	// Pending and processing operations are never pruned.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// Claimer is the worker side; each call is its own short transaction.
//
// Fencing: Progress, Complete and Fail act only on a PROCESSING row; on any other status they change
// nothing and return domain.ErrFailedPrecondition. On a cancel-requested row, Complete and Fail move it to
// CANCELLED instead (no event) and return domain.ErrCancelled. An unknown id (for example the delete_bank
// job whose final transaction deleted its own row with the bank) returns domain.ErrNotFound, which the
// runner treats as done for delete_bank. A worker that lost its job (reclaimed and re-claimed elsewhere)
// learns it from Heartbeat's *LostJobsError and must stop the handler.
//
// Runner obligation: keep heartbeating an id until its handler returns, and on every exit (panic
// included) defer Complete or Fail. The runner owns the attempt cap: at attempt+1 >= MaxAttempts it calls
// Fail(nil); the Claimer never caps a Fail with retryAt.
type Claimer interface {
	// Claim takes up to n due jobs of kinds with FOR UPDATE SKIP LOCKED, skipping a job whose
	// serial key has a PROCESSING row or an older due PENDING row, and
	// skipping deleting banks (except their delete_bank job). Claimed rows become PROCESSING with
	// worker_id = worker and a fresh heartbeat; jobs come back oldest first. A lost race on the unique
	// processing index is retried inside Claim and never surfaces as an error.
	Claim(ctx context.Context, worker string, kinds []domain.OperationKind, n int) ([]Job, error)
	// Heartbeat refreshes heartbeat_at on the given jobs still PROCESSING under worker (cancel-requested
	// ones included, so their serial key stays held while the handler winds down). It returns a
	// *LostJobsError naming every id whose cancel was requested or that worker no longer holds (reclaimed,
	// finished or re-claimed elsewhere), after refreshing the rest; the runner stops those handlers.
	Heartbeat(ctx context.Context, worker string, ids []domain.OperationID) error
	// Progress records the job's latest checkpoint (fenced: PROCESSING rows only).
	Progress(ctx context.Context, id domain.OperationID, p domain.Progress) error
	// Alive reports whether the job should keep running: true only while it is PROCESSING without a cancel
	// request and its bank is neither tombstoned nor gone (a delete_bank job stays alive). An unknown id is
	// (false, nil).
	Alive(ctx context.Context, id domain.OperationID) (bool, error)
	// Complete marks the job COMPLETED (and finished) with its result and per-slot usage (fenced).
	Complete(ctx context.Context, id domain.OperationID, result json.RawMessage, usage []domain.Usage) error
	// Fail records cause on the job (fenced). retryAt != nil returns it to PENDING with run_at = *retryAt
	// and attempt+1; nil = terminal FAILED, which also appends an OPERATION_FAILED lifecycle event under a
	// fresh bank change seq (skipped when the bank is tombstoned or gone). A failed refresh_mental_model is
	// paused and recorded as REFRESH_FAILED by an idempotent reconcile in the maintenance tick,
	// not by the Claimer.
	Fail(ctx context.Context, id domain.OperationID, cause error, retryAt *time.Time) error
	// ReclaimStale handles PROCESSING jobs whose heartbeat is older than ttl. Below their kind's max
	// attempts (domain.OperationKind.MaxAttempts; 0 = unbounded) they return to PENDING with attempt+1 and
	// run_at unchanged. At attempt+1 >= max they become FAILED with exactly one OPERATION_FAILED event, as
	// with Fail(nil); on a tombstoned or gone bank they still become FAILED, without the event, and the
	// sweep never fails because of one bank. Stale cancel-requested rows become CANCELLED. Terminal rows
	// are never touched. It returns how many rows it changed.
	ReclaimStale(ctx context.Context, ttl time.Duration) (int, error)
}
