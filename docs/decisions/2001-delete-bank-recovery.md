# 2001 — Recover stalled bank deletes from the maintenance tick; delete_bank can't be cancelled

## Context
The WP-13 ruling made delete_bank uncapped, but only the handler's error path enqueues a continuation
(decision 1451). ReclaimStale and the runner's shutdown release still charge attempts and FAIL the row at
`MaxAttempts` (3), with no continuation. Three crashes or rolling deploys during one delete therefore leave
the bank tombstoned forever (final review B, MEDIUM): writes fail with FAILED_PRECONDITION and reads with
NOT_FOUND until someone re-issues DeleteBank. A Cancel of the delete_bank row had the same effect, and so
did a Retry of another job on a deleting bank, which queued work that claims never pick up.

## Decision
- **The read.** A new Postgres store method, `(*postgres.Store).StalledBankDeletes(ctx)`, lists the
  tombstoned banks with no `pending` or `processing` delete_bank. It is consumed through
  `worker.StalledDeleteSource` (the decision 1452 pattern; no port change). A parked continuation with a
  future `run_at` is pending, so it counts as active.
- **The tick step.** `resume_bank_deletes` runs right after `reclaim_stale`, so a row that reclaim failed
  is resumed in the same tick.
  - It enqueues delete_bank with serial key `bank`, payload `{}` and DedupePendingOrRunning, so concurrent
    ticks insert one row.
  - It takes no `tx.Seq`, which refuses tombstoned banks (Tombstone doesn't take one either).
  - A bank that finished deleting in the meantime (NOT_FOUND) is skipped.
- **A delete that keeps failing for good** (a permanent error) is re-enqueued on every tick: one operation
  row a minute until the cause is fixed. This is acceptable for an early iteration because nothing is lost
  and the rows are pruned with the others. A backoff is not implemented.
- **Cancel** of a delete_bank operation is FAILED_PRECONDITION.
- **Retry** on a deleting bank is FAILED_PRECONDITION (domain.ErrBankDeleting) for every kind except
  delete_bank. Retrying a FAILED delete_bank stays allowed as the manual recovery path.
- The check reads the bank without a lock. A Retry that races DeleteBank can leave one pending job on a
  deleting bank. Claims skip it and delete_bank removes it, so this is harmless.

## Spec lines involved
SPEC "Behavioural rules" (DeleteBank; writes return FAILED_PRECONDITION), "Operations" (Cancel, Retry),
"Jobs" (maintenance tick, retries); decisions 1306, 1451 and 1452.
