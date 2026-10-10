# 112: The claim and its retry

Status: accepted (WP-01)

## Context

The port says that a lost race on the unique processing index "is retried inside Claim and never surfaces as
an error". The SPEC's claim SQL must be used verbatim. sqlc infers `o.kind = ANY($1)` as a scalar `text`
parameter, which can't carry the kinds list.

## Decision

- `ClaimSelect` (queries/queue.sql) is the spec's SQL with one change: `$1` is written `$1::text[]`. The cast
  does not change the semantics, and it is the only difference.
- The follow-up update is `UPDATE operations SET status = 'processing', worker_id = $worker, heartbeat_at =
  now(), updated_at = now() WHERE id = ANY($ids)`. It adds `updated_at` so the client-visible Operation shows
  the transition.
- Jobs come back in the SELECT's order (oldest first), not in UPDATE order.
- Claim retries the whole claim, each attempt in a new transaction, when that transaction fails with:
  - a unique violation on `operations_processing_serial_key_uniq`, or
  - a deadlock (40P01). Two claimers that update rows of overlapping keys in opposite order can deadlock
    while each waits on the other's uncommitted index entry.
- The bound is 5 tries. When they are all used up, Claim returns an empty batch and no error. The runner polls
  again anyway. Any other error is returned.
- `n <= 0` or no kinds returns an empty batch without querying.

## Alternatives

- Retry only the UPDATE inside one transaction with a savepoint. Rejected: it is more code, and the spec says
  the loser "claims again".
- Running the verbatim string through pgx outside sqlc. Rejected: it would be the only query outside sqlc,
  for a cast that changes nothing.

## Spec lines

SPEC "Jobs" 1926-1945 (claim SQL, unique index). `internal/port/jobs.go` Claimer.Claim. Decision 004.
