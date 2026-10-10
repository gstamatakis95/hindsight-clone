# 501 — Runner slots, lost jobs, shutdown and results

## Context
SPEC "Jobs" sets the runner's shape: 10 slots, 2 of them reserved for consolidation, a 500 ms poll and a
15 s heartbeat. Decision 012 (R7, R8') adds two obligations: keep heartbeating an id until its handler
returns, and defer Complete or Fail on every exit. The spec leaves open:
- how the reserved slots are counted;
- what the terminal call is for a lost job;
- what happens to running jobs on shutdown;
- how a handler, whose signature only returns an error, reports a result and usage for `Complete`.

## Decision
- **Slots.** Non-consolidate jobs may hold at most `Slots − ReservedConsolidate` (8) slots, counted on
  running jobs. Consolidate may use any free slot. Each poll claims consolidate first, then the other
  registered kinds. Only kinds with a registered handler are claimed. A finished job wakes the poll loop
  at once.
- **Lost jobs.** A `*LostJobsError` from Heartbeat means the cancel was requested, or the job was
  reclaimed or re-claimed elsewhere. The runner cancels that handler's context. When the handler
  returns, the runner calls `Alive` first.
  - If `Alive` is true, the row is PROCESSING under another worker, so the runner writes nothing.
  - Otherwise it calls `Fail(cause, nil)`. On a cancel-requested row this ends CANCELLED; on a reclaimed
    or finished row it is fenced and changes nothing.

  The job is never requeued from a lost run, so a job already re-claimed by another worker can't get a
  second concurrent run. The R7 window (a write landing on a re-claimed row) narrows to the race between
  `Alive` and `Fail`.
- **Shutdown.** When `Run`'s context ends, the runner stops claiming and cancels every handler with a
  shutdown cause. It waits for all of them and heartbeats until the last one returns. A handler that
  cancelled by the shutdown is released with `Fail(cause, retryAt = now)`, outside the attempt cap: an
  interrupted run is not a failure, and it re-runs at once on another worker. The release is decided on
  the cancel cause alone, whatever error the handler returned (engines often drop `context.Canceled`),
  unless that error is `ErrPermanent`. A handler that returns nil during shutdown is completed.
- **Settling.** Complete and Fail run under `context.WithoutCancel` with a 30 s timeout, so they also run
  during shutdown. Fencing outcomes are logged, not treated as errors:
  - `ErrCancelled`;
  - `ErrNotFound` for delete_bank;
  - `ErrFailedPrecondition`.

  Any other error leaves the row PROCESSING without heartbeats, and ReclaimStale re-queues it. Handlers
  are idempotent, so nothing is lost.
- **Results.** `ReportResult(ctx, result, usage...)` records a handler's result summary and per-slot usage
  for `Complete`. Without it, the Operation result and usage fields could never be filled.
- **Interface.** `*Runner` implements a small `JobRunner` interface (Handle, Run). `Register` takes that
  interface; the spec's `Register(*Runner, Handlers)` still compiles.
- **Defaults.** A zero `RunnerConfig` field takes its default; `Clock` defaults to the system clock.

## Alternatives
- Count reserved slots per poll on claimed jobs only. Rejected: running jobs are what starve
  consolidation.
- Release lost jobs with a retry. Rejected: that makes a row PENDING while another worker runs it, which
  is a double run.
- Leave jobs PROCESSING on shutdown and let reclaim pick them up. Rejected: that delays them by 2 minutes
  and charges an attempt the same way.

## Spec lines involved
SPEC "Jobs" (Slots, Heartbeats and reclaim), "Behavioural rules" (Operations), "Function signatures"
(worker), decision 012 R7/R8'/R10'.
