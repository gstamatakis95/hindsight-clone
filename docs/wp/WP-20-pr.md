# WP-20: final-review fix-up for the worker, the maintenance tick and cmd/memd

Branch `wp/WP-20-worker-hardening`, rebased on main after WP-19.

## Built
- [x] 1. **Maintenance-tick consolidation** (orchestrator ruling, SPEC 908 vs 1953):
  - The tick skips banks whose effective config has `auto_consolidation=false` or `enable_observations=false`.
    It gets them through `MaintenanceDeps.Config`, a ConfigResolver.
  - It reconciles only units created before `now - consolidation grace`. The knob is
    `MEMD_CONSOLIDATION_RECONCILE_GRACE`, default 10m.
  - A new store read, `UnconsolidatedBanks(ctx, before)`, backs it (decision 2000).
- [x] 2. **delete_bank recovery:**
  - A new tick step, `resume_bank_deletes`, runs right after reclaim. It re-enqueues delete_bank for
    tombstoned banks that have no pending or processing delete_bank, using the store read
    `StalledBankDeletes`.
  - Cancel of a delete_bank returns FAILED_PRECONDITION.
  - Retry of any other kind on a deleting bank returns FAILED_PRECONDITION (decision 2001).
- [x] 3. **LOWs:**
  - The tick enqueues graph_maintenance for banks with graph work, using the store read
    `BanksWithGraphWork` (decision 2002).
  - The consolidate retry is skipped only for automatic jobs.
  - The refresh, rederive and retag handlers write progress at a start checkpoint, and stop there when the
    job was cancelled or the bank is being deleted.
  - Retain checks the bank isn't deleting before the blob Put (decision 2003).
- [x] 4. **cmd/memd:**
  - The worker id is `<MEMD_WORKER_ID or hostname>-<uuid>` per process.
  - Both S3 keys are required.
  - Startup fails when EXTRACT or REFLECT is unbound.
  - An unbound slot returns FAILED_PRECONDITION, and is still a permanent job failure.
  - `MEMD_GATEWAY_MAX_CONCURRENT_<SLOT>` fills `gateway.Config.SlotConcurrency`.
  - Setting `MEMD_WORKER_HEALTH_ADDR` serves grpc.health from the worker (decision 2004).
- [x] 5. **Errors and surface:**
  - CreateBank on a deleting bank returns FAILED_PRECONDITION.
  - An existing bank returns a clean ALREADY_EXISTS, and a unique violation no longer leaks SQL text.
  - Tenant and bank ids "." and ".." are rejected.
  - The health Watch and List semantics are documented on `healthServer` (decision 2004).
- [x] 6. **e2e:**
  - BankDelete stops the worker across DeleteBank and asserts a strict FAILED_PRECONDITION write.
  - It asserts that the `e2e/sc-delete/` MinIO prefix is non-empty before the delete and empty after it.
  - The README Status line is refreshed.

No port, domain, proto or migration change, and no CCR. The three store reads follow the pattern of
decisions 1452 and 1453: they are Store methods consumed through interfaces declared in the worker, and
`cmd/memd/wire.go` narrows the store to `maintenanceReads`.

## Acceptance tests (all pass)

| Item | Test |
|---|---|
| 1 | `worker.TestConsolidationReconcileSkipsDisabledBanks`, `worker.TestConsolidationReconcileGrace`, `postgres_test.TestUnconsolidatedBanks` |
| 1 (stays green) | `worker.TestConcurrentMaintenanceNoDuplicateEnqueues`. Only its fake's method was renamed. |
| 2 | `worker.TestMaintenanceResumesStalledBankDelete` (ReclaimStale fails the row at its cap, then the same tick re-enqueues exactly one), `worker.TestControlDeleteBankAndDeletingBank`, `postgres_test.TestStalledBankDeletes` |
| 3 | `worker.TestMaintenanceEnqueuesGraphMaintenance`, `postgres_test.TestBanksWithGraphWork`, `worker.TestManualConsolidateRetriedDespitePending`, `worker_test.TestHandlersReportProgress`, `retain.TestIngestDeletingBankStoresNoBlob` |
| 4 | `main.TestEffectiveWorkerID`, `main.TestValidateS3Keys`, `main.TestRequiredSlots`, `main.TestUnboundSlotRouter`, `main.TestSlotConcurrencyEnv`, `main.TestWorkerHealthServer` |
| 5 | `bank.TestCreateBankDeletingAndExisting`, `server.TestCheckNameRejectsDotIDs` |
| 6 | `e2e.TestScenarios/BankDelete` |

Gates on the rebased branch:
- `make gen` leaves no diff.
- `make lint BREAKING_AGAINST=contracts-v1` reports 0 issues.
- `make test` passes, including the Postgres suites.
- `make e2e` passes, and no compose projects are left over.

## Decisions
- 2000: tick consolidation config filter and grace. The grace is a process knob, not a BankConfig field,
  because BankConfig is frozen and the tick has no request to override from.
- 2001: delete_bank recovery, plus the cancel and retry rules.
- 2002: graph-maintenance reconcile.
- 2003: manual-consolidate retry, handler progress, and the retain bank check.
- 2004: cmd/memd hardening and the API error surface.

## Known gaps
- Progress is written once, at the start of refresh, rederive and retag. Progress between the turns of an
  agentic refresh needs checkpoint parameters in the mentalmodel, curate and docs engines, which are outside
  WP-20's paths.
- A delete_bank that fails permanently is re-enqueued on every tick, one row a minute, with no backoff.
- A retain racing DeleteBank in the window between the bank check and the Put can still leave an orphan
  blob. The window is now one read transaction wide.
- `port.Reader.BanksToConsolidate` no longer has a caller. It stays because it is a frozen port method.
