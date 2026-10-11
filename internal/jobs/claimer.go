package jobs

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// Claimer is the worker side; each call is its own short transaction.
//
// Fencing: Progress, Complete and Fail act only on a PROCESSING row; on any other status they change
// nothing and return domain.ErrFailedPrecondition. On a cancel-requested row, Complete and Fail move it to
// CANCELLED instead (no event) and return domain.ErrCancelled. An unknown id (for example the delete_bank
// job whose final transaction deleted its own row with the bank) returns domain.ErrNotFound, which the
// runner treats as done for delete_bank. A worker that lost its job (reclaimed and re-claimed elsewhere)
// learns it from Heartbeat's *store.LostJobsError and must stop the handler.
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
	Claim(ctx context.Context, worker string, kinds []domain.OperationKind, n int) ([]store.Job, error)
	// Heartbeat refreshes heartbeat_at on the given jobs still PROCESSING under worker (cancel-requested
	// ones included, so their serial key stays held while the handler winds down). It returns a
	// *store.LostJobsError naming every id whose cancel was requested or that worker no longer holds (reclaimed,
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
