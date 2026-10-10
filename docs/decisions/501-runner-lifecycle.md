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
- **Runs, not ids.** Each running entry is one run, and its pointer is the run's token. If a claim
  returns an id this runner is still running (its job was reclaimed after a heartbeat stall and claimed
  back here), the older run is cancelled as lost and the newer run replaces it. A run's release removes
  only its own entry, so the older run never drops the newer one's slot or heartbeat.
- **Lost runs.** The runner stops a run as lost in three cases:
  - Heartbeat names it in a `*LostJobsError` (cancel requested, reclaimed, re-claimed elsewhere);
  - its last successful heartbeat is older than ¾ of `StaleAfter` (2 min). Each Heartbeat call is
    bounded by `HeartbeatInterval`. Fencing itself first means a hung or partitioned worker stops before
    another worker can reclaim and re-run the job;
  - this runner re-claims its id.

  The lost check comes before success, so a lost handler that returns nil is not completed unchecked.
  The runner then calls `Alive`:
  - `Alive` errors: write nothing; ReclaimStale covers the row;
  - `Alive` is true: the row is PROCESSING under another run, here or on another worker, so write
    nothing;
  - otherwise: a successful run is completed and a failed one gets `Fail(cause, nil)`. On a
    cancel-requested row either call ends CANCELLED; on a reclaimed or finished row it is fenced and
    changes nothing.

  A lost run is never requeued, so no job gets a second concurrent run. The R7 window (a write landing on
  a re-claimed row) narrows to the race between `Alive` and the write.
- **Shutdown** (amended in review round 1, H3). When `Run`'s context ends, the runner:
  1. stops claiming;
  2. gives the running handlers `ShutdownGrace` (default 30 s) to finish on their own, so they complete
     normally;
  3. cancels the rest with a shutdown cause, waits for them, and heartbeats until the last one returns.

  A handler cut off by the shutdown is released with `Fail(cause, retryAt = now)` and re-runs at once on
  another worker. The release is decided on the cancel cause alone, whatever error the handler returned
  (engines often drop `context.Canceled`), unless that error is `ErrPermanent`. The release is *not*
  outside the attempt cap: the frozen `Fail(retryAt)` always charges an attempt, so repeated shutdowns
  use up a job's retry budget. The runner itself never fails a job for good on a shutdown release, and
  the grace period keeps such releases rare. (Orchestrator ruling: documented, no CCR.)
- **Settling.** Complete and Fail run under `context.WithoutCancel` with a 30 s timeout, so they also run
  during shutdown. Transient errors are retried with backoff (50 ms doubling to 2 s) until that timeout.
  Fencing outcomes end the call at once and are logged, not treated as errors:
  - `ErrCancelled`;
  - `ErrNotFound` (expected for delete_bank);
  - `ErrFailedPrecondition`.

  If the timeout runs out, the row stays PROCESSING without heartbeats, and ReclaimStale re-queues it.
  Handlers are idempotent, so nothing is lost.
- **Claims** run under the runner's long-lived context with a 10 s timeout, after a check that the run
  context is still live. A claim that commits as shutdown begins therefore still starts its jobs.
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
- Release shutdown jobs without charging an attempt. Rejected: it needs a port change (orchestrator
  ruling: documentation route).

## Spec lines involved
SPEC "Jobs" (Slots, Heartbeats and reclaim), "Behavioural rules" (Operations), "Function signatures"
(worker), decision 012 R7/R8'/R10'.
