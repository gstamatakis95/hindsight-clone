# 113: Terminal failures, OPERATION_FAILED events and ReclaimStale

Status: accepted (WP-01)

## Context

`Fail(nil)` and ReclaimStale at max attempts append one OPERATION_FAILED event under a fresh bank change seq.
They skip the event on a tombstoned or gone bank, and the sweep must never fail because of one bank. The
event's `subject_id`, the error text of a reclaimed failure and the lock order are not specified.

## Decision

- **Event shape:** `type = 'operation_failed'`, `operation_id` = the job id, and `subject_id = ''`. The
  operation is already carried by `operation_id`, and the adapter does not interpret payloads to find a
  document or model id. `seq` comes from the same bump as `Tx.Seq`, which also stamps
  `last_memory_write_at`. `at` is the database's `now()`.
- **Lock order** (bank row first):
  1. Read the job unlocked.
  2. Unless its cancel was requested, bump the bank seq. A tombstoned or gone bank yields no seq and no error.
  3. Run the fenced `UPDATE ... WHERE status = 'processing'`. It sets `failed`, or `cancelled` if a cancel
     request landed in between.
  4. Insert the event only when the row ended FAILED and a seq was taken.
  5. If the fenced update matches nothing, roll back the transaction, seq bump included, and return
     `ErrNotFound` (row gone) or `ErrFailedPrecondition`.
- **ReclaimStale:**
  1. Scan PROCESSING rows whose `heartbeat_at < now() - ttl`, using database time.
  2. Classify each row in Go with `OperationKind.MaxAttempts()`:
     - cancel-requested rows go to CANCELLED;
     - rows with `MaxAttempts == 0` or `attempts + 1 < MaxAttempts` go back to PENDING with attempts+1 and
       `run_at` unchanged;
     - all other rows become FAILED.

     Re-queued rows have `worker_id` and `heartbeat_at` cleared.
  3. Re-queue and cancel each run as one guarded statement. Each FAILED row gets its own short transaction,
     guarded by "still stale and still at the attempt seen by the scan".

  Rows that changed in the meantime are skipped silently. Any other per-row error is collected, the sweep
  goes on, and the errors are returned joined, together with the count, at the end. The count includes
  re-queued, cancelled and failed rows.
- **Error text:** a reclaimed failure records `worker heartbeat lost on attempt N of M`. A terminal
  `Fail` with a nil cause keeps the previous error.
- **Fail with a retry time** on a cancel-requested row: the row becomes CANCELLED, keeps its attempts and gets
  no event, and the call returns `ErrCancelled`.
- **Complete** stores the result and usage on a cancel-requested row before it becomes CANCELLED, so the work
  that was done stays visible. Nil usage is stored as `[]`.

## Alternatives

- Use the op id as `subject_id`. Rejected: it duplicates `operation_id`.
- Fail every reclaimed row in one statement. Rejected: one bank's seq bump failing would abort the whole
  sweep.

## Spec lines

SPEC "Jobs" heartbeats and retries (1947-1950), "Behavioural rules" change sequence. `internal/port/jobs.go`
Claimer doc. Decision 012 R7, R8', R10'.
