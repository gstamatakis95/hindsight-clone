# 1451 — Job handlers: continuations, the graph re-check and delete_bank without an attempt cap

## Context
WP-05 left the handlers as stubs. The carried items:
- the consolidate handler must not re-enqueue on More (decision 1000);
- delete_bank and graph_maintenance re-enqueue themselves (DedupePending) when a run is not done;
- graph_maintenance re-checks `graph_dirty` and `entity_maintenance` "after Complete";
- delete_bank retries with no attempt cap, like consolidate.

`domain.OperationKind.MaxAttempts` is frozen and caps every kind except consolidate at 3. The runner calls
`Complete` only after the handler returns.

## Decision
- **Handlers are thin.**
  - Each calls its engine and reports the result and usage with `ReportResult`.
  - Engine errors are wrapped with `%w`, so the retry policy still sees the typed error.
  - A nil engine fails its job with `ErrPermanent`.
- **consolidate** never re-enqueues; `Run` already queued the continuation.
- **delete_bank.**
  - A run with work left enqueues a continuation, which joins one the deleter queued itself.
  - On a transient error at the kind's last capped attempt, the handler first enqueues a continuation with
    capped exponential backoff, then returns the error. The row ends FAILED with its error visible, and the
    delete goes on in the new row.
  - Permanent and cancellation errors leave no continuation.
- **graph_maintenance.**
  - With budget left over work, it re-enqueues.
  - When the run reports done, the handler's last step reads `Dirty` and `PendingMaintenance` (limit 1) in
    one transaction. If either has rows, it re-enqueues.
  - This catches a removal that committed after the maintainer's final empty batch and joined the running
    row through DedupePendingOrRunning.
- **The continuation** uses the same kind, serial key and payload, DedupePending, and no `tx.Seq`: it is not
  memory content, and Seq refuses a tombstoned bank. A continuation for a gone bank is dropped.

## Alternatives
- **A re-check after Complete.** This needs a runner hook (internal/worker/runner.go is not this card's).
  The window left is between the handler returning and Complete, which is a few milliseconds. Work that
  lands in it waits for the next removal or tick.
- **A CCR to uncap delete_bank in `MaxAttempts`.** Rejected: it changes a frozen path for something the
  handler can do. A crash-reclaim past the cap still fails the row. Re-issuing DeleteBank resumes it
  (decision 1306).

## Spec lines involved
SPEC "Jobs" (bounded runs, retries), "Function signatures" (`Handlers`), decisions 1000, 1304, 1306; WP-13
carried items.
