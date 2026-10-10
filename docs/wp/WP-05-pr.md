# WP-05 · Worker runtime

Branch `wp/WP-05-worker-runtime`, based on `contracts-v1`. Touches only `internal/worker/**`, its
regenerated mocks (`internal/mocks/worker/mocks.go`), `docs/wp/WP-05*.md` and `docs/decisions/50x-*.md`.
No frozen path changed and no CCR.

## Built (card "Build")
- [x] **Runner** (`runner.go`, `exec.go`):
  - polls every 500 ms, and immediately when a job finishes;
  - 10 slots, 2 of them reserved for consolidate. Non-consolidate jobs are capped at 8 running, and
    consolidate is claimed first and may use any slot;
  - dispatches by kind and claims only registered kinds;
  - heartbeats every 15 s until every handler has returned and been settled;
  - stops a handler on `*LostJobsError`;
  - recovers panics;
  - defers Complete or Fail on every exit, including during shutdown.
  - `*Runner` implements `JobRunner`.
- [x] **Retry policy** (`retry.go`, `errors.go`):
  - 60 s × (attempt+1), with the runner owning the cap: `Fail(nil)` at attempt+1 >= `MaxAttempts()`;
  - consolidate backs off 60 s × 2^attempt, capped at 30 min, with no attempt limit;
  - consolidate skips its retry (ends FAILED) when another consolidate is pending for the bank
    (`PendingChecker` over `Queue.List`);
  - `ErrPermanent`, `ErrRetryAt` / `RetryAt()`.
- [x] **Checkpoint** (`NewCheckpoint`): Progress, then Alive. It returns `domain.ErrCancelled` when the ctx
  is done, Progress is fenced or the row is gone, or Alive is false.
- [x] **OperationControl** (`NewOperationControl`): Cancel and Retry, each in its own short transaction
  over `Queue.Cancel` / `Queue.Retry`.
- [x] **`RunMaintenance`**, every 60 s with no leader. Steps, in order:
  1. `ReclaimStale(2m)`;
  2. the refresh-failure reconcile (carried-forward R10' item; consumer interface `RefreshFailureReconciler`
     for WP-12);
  3. cron refreshes (consumer interface `CronRefresher` for WP-12);
  4. the consolidation reconcile (`ConsolidationCandidates`, then a `DedupePending` enqueue per bank in its
     own tx);
  5. prune terminal operations (`PruneOperations`) and events.

  A failing step never stops the others.
- [x] **Handler stubs** (`handlers.go`) and `Register`. Each stub fails with `ErrPermanent` until WP-14
  wires the engines. retain_batch is not registered.
- [x] **`ReportResult(ctx, result, usage...)`**, so handlers can fill the Operation's result and usage via
  Complete.

## Acceptance tests
All run against a test-local fake Claimer/Queue/Store and a manual Clock (`internal/worker/fake_test.go`).

| Card "Accept" | Test | Result |
|---|---|---|
| reserved slots hold under a retain flood | `TestRunnerReservedSlotsHoldUnderRetainFlood` | pass |
| retries are scheduled at the right times | `TestRunnerRetrySchedule`, `TestRetryPolicy`, `TestRunnerConsolidateSkipsRetryWhenPending` | pass |
| a cancelled job stops at its next checkpoint | `TestRunnerCancelledJobStopsAtCheckpoint` (Alive path and heartbeat/lost path) | pass |
| a worker killed mid-job gets its job reclaimed | `TestKilledWorkerJobReclaimed` | pass |
| three concurrent maintenance loops produce no duplicate enqueues | `TestConcurrentMaintenanceNoDuplicateEnqueues` | pass |

Supporting tests:
- `TestRunnerPanicRetries`
- `TestRunnerShutdownReleasesJobs`
- `TestRunnerReportsResult`
- `TestRegisterStubs`
- `TestMaintenanceStepOrderAndIsolation`
- `TestPruneOperations`
- `TestCheckpointReport`
- `TestOperationControl`
- `TestPendingChecker`

Gates:
- `make gen` leaves no diff;
- `make lint` and `make test` pass;
- `go test -race -count=3 ./internal/worker/` passes, with 93% statement coverage.

## Decisions and CCRs
- `docs/decisions/500-worker-retry-policy.md`: attempt numbering, the consolidate backoff cap, which
  errors are permanent, `ErrRetryAt` still counting toward the cap, and the skipped consolidate retry
  ending FAILED.
- `docs/decisions/501-runner-lifecycle.md`: slot accounting, lost jobs failed without requeue (so no
  double run), shutdown releasing jobs with `retryAt = now` outside the cap, `ReportResult`, and the
  `JobRunner` interface.
- `docs/decisions/502-maintenance-consumer-interfaces.md`: the consumer interfaces, the step order, the
  consolidate job shape (payload `{}`, serial key `consolidate`, `DedupePending`) and the retention
  defaults (7 d operations, 30 d events).
- `docs/decisions/503-prune-operations-clock.md`: `PruneOperations` takes a `port.Clock` (a deviation
  from the spec signature, so the code doesn't call `time.Now`).
- No CCRs.

## Known gaps
- **Test fakes are local.** The tests use fakes in `internal/worker/fake_test.go`, not `internal/fake`,
  because WP-02 runs in parallel. Swap them once WP-02 lands; the shared porttest suite then guarantees
  the semantics.
- **Interfaces with no implementation yet.** `ConsolidationCandidates`, `CronRefresher` and
  `RefreshFailureReconciler` exist only as interfaces. Their steps are skipped while nil. WP-14 must wire:
  - `ConsolidationCandidates`: an adapter query, or a CCR'd Reader method;
  - `CronRefresher` and `RefreshFailureReconciler`: WP-12.
- **Handlers are stubs** (by the card); WP-14 fills them.
- **R7 known limit still applies.** A Complete or Fail from a worker whose job was reclaimed *and*
  re-claimed elsewhere can land on the other worker's row, because Complete and Fail carry no worker
  token. The runner narrows this: lost jobs are never requeued.
- **Shutdown counts as an attempt.** A job released on shutdown still gets attempt+1 (`Fail` with retryAt
  always increments). Only the cap check is skipped for that call.
- **Lint toolchain.** `golangci-lint` on the default PATH here resolves to a v1 binary in `~/go/bin`. The
  gates were run with the v2 binary (`/opt/homebrew/bin`) first on PATH.
