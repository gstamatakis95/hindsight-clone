# WP-14b blockers

No blocker is open. Two gaps outside this card's Owns blocked or weakened carried items. Both are closed with
additive Postgres adapter queries, which the worker consumes through its own interfaces. No port, domain,
migration or schema change was made. The orchestrator decides whether to keep these fixes or hand them to an
adapter work package.

## 1. retain_batch parents never leave PENDING (closed by decision 1453)
- **Failing test (before the fix):** `e2e.TestScenarios/RetainAsync`. It failed with "bank sc-retain-async
  not idle: pending {retain_batch: 1}".
- **Cause:**
  - The ingestor enqueues a retain_batch parent for an async multi-document retain.
  - No worker runs it, and nothing in the adapters or engines updates its status.
  - So `BankStats.pending_operations_by_kind` never empties, the barrier hangs, and the parent is never pruned.
  - This contradicts SPEC "Operations": the parent's status aggregates its children.
- **Fix:**
  - `(*postgres.Store).SettleRetainBatches` is one idempotent UPDATE
    (`internal/adapter/postgres/settle.go`, `queries/settle.sql`).
  - The maintenance step `settle_retain_batches` runs it (`internal/worker/batches.go`, two lines in
    `maintenance.go`).
  - Tests: `postgres.TestSettleRetainBatches`, `postgres.TestSettleRetainBatchFailedChild`, and
    `e2e.TestScenarios/RetainAsync`, which now passes.

## 2. Stranded-fact rederive tick had no read to drive it (closed by decision 1452)
- **The carried item:** "Add a tick step that re-enqueues rederive for live facts with a NULL embedding
  (stranded by a permanently failed rederive)".
- **The gap:** no port read lists such facts across banks.
- **Fix:**
  - `(*postgres.Store).StrandedFacts` (`internal/adapter/postgres/stranded.go`, `queries/stranded.sql`)
    starts from FAILED or CANCELLED rederive operations and joins units whose embedding is NULL.
  - The maintenance step `rederive_stranded` uses it (`internal/worker/stranded.go`).
  - Tests: `postgres.TestStrandedFacts`, `worker.TestMaintenanceRederivesStrandedFacts`.
