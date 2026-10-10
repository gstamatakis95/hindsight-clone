# 110: Enqueue dedupe details in the Postgres queue

Status: accepted (WP-01)

## Context

`port.Queue.Enqueue` and decisions 004/012 fix the dedupe mechanism (`joinable`, the per-key advisory lock,
FOR UPDATE on a joined row, FOR KEY SHARE on the bank row). A few edge cases are left open: several dedupe
keys in one transaction, a dedupe Enqueue without a serial key, a refresh asked to dedupe as
PendingOrRunning, re-cancelling a cancel-requested row, and whether a retried row is joinable.

## Decision

- **Several keys in one transaction.** Each `Enqueue` takes its own advisory lock, so the adapter can't sort
  the keys of one caller's transaction. Two transactions that take the same two keys in opposite order can
  deadlock. Postgres aborts one of them with 40P01; `Store.InTx` rolls it back and runs it again, up to 3
  attempts (decision 100; the port doc allows "keys in sorted order ... or retry on 40P01"). Only after the
  last attempt does the caller see `domain.ErrConflict`. A caller
  that enqueues several dedupe jobs in one transaction should enqueue them in sorted key order to avoid this.
- **Effective policy**, in order:
  1. A job without a serial key never dedupes. Its row is inserted with `joinable = false`. A NULL key would
     take no lock and match no row (decision 004: "dedupe enqueues always carry a serial key").
  2. A `refresh_mental_model` job always dedupes as `DedupePending`. This applies whatever the requested
     policy (None and PendingOrRunning included) and whatever the overrides.
  3. A job with overrides never dedupes.
  4. Otherwise the requested policy applies.

  A row is inserted with `joinable = true` exactly when the effective policy is not None.
- **Lock order.** The order is: FOR KEY SHARE on the bank row, then the advisory lock in its own statement,
  then the lookup with `FOR UPDATE ... LIMIT 1` (oldest joinable match first), then the INSERT.
  - If a claim takes the matched row while the lookup waits, READ COMMITTED re-checks the row. The
    now-processing row drops out under `DedupePending`, and the enqueue inserts a new row.
- **Cancel of a cancel-requested row** succeeds again and returns the PROCESSING row. It keeps the first
  `cancel_requested_at`.
- **Retry** sets `joinable = joinable OR kind = 'refresh_mental_model'`:
  - refresh rows are always joinable;
  - a cancelled row of another kind stays non-joinable, because Cancel cleared the flag and the original
    policy is not stored.

  Retry also clears `worker_id` and `heartbeat_at`.
- **Payload.** A nil payload is stored as `{}`. Upgrade OR-s only `force_full`, as the port doc says. Decision
  012's broader "a boolean is OR-ed" is read as describing that flag.

## Alternatives

- Return `ErrInvalidArgument` for a dedupe Enqueue without a serial key. Rejected: it would add a failure mode
  that the port doc does not list.
- Batch several keys in one sorted call. Rejected: the port has no batch Enqueue.

## Spec lines

SPEC "Jobs" (1903-1955), dedupe column of the kinds table. `internal/port/jobs.go` Queue.Enqueue/Upgrade.
Decisions 004 ("Operations: dedupe, cancel and the claim") and 012 (R1, R2, R8').
