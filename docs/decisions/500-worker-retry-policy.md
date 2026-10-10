# 500 — Worker retry policy details

## Context
SPEC "Jobs" says: transient errors back off by 60 s × attempt, with 3 attempts. Consolidation retries
indefinitely with capped exponential backoff, and skips the retry if another consolidation is already
pending for the bank. Deterministic errors fail at once.
Decision 012 R10' says the runner owns the attempt cap: at attempt+1 >= MaxAttempts it calls
`Fail(nil)`. The spec leaves open:
- how attempts are numbered;
- what the cap and the base of the exponential backoff are;
- what `ErrRetryAt` does to the attempt count;
- which errors count as deterministic;
- what "skip the retry" leaves on the row.

## Decision
- **Numbering.** `Job.Attempt` is 0 on the first run. A failed run of attempt `a` retries at
  `now + 60 s × (a+1)`, which gives 60 s and then 120 s. Once `a+1 >= MaxAttempts()` (3), the job fails
  for good. ReclaimStale uses the same rule. In total that is three runs, then FAILED.
- **Consolidate.** `MaxAttempts() == 0`, so the delay is `60 s × 2^a`, capped at 30 min
  (`ConsolidateBackoffCap`): 1, 2, 4, 8, 16, 30, 30, ... minutes, with no limit on attempts.
- **`ErrRetryAt{At, Cause}`** replaces the backoff time when `At` is set. It still counts as an attempt,
  because `Claimer.Fail` with retryAt increments the attempt, so the per-kind cap still applies.
- **Permanent errors** fail at once with no retry:
  - `ErrPermanent`, which handlers wrap for schema violations, integrity errors and bad payloads;
  - `domain.ErrInvalidArgument`;
  - `domain.ErrCancelled`, the checkpoint's cancellation. Its terminal `Fail` moves a cancel-requested row
    to CANCELLED.
- **A panicking handler** is recovered and treated as a transient failure, so it gets a retry within the
  cap.
- **Skipping the consolidate retry** (amended in review round 1, M2). If a failed consolidate would
  retry, the runner first checks for another consolidate in the bank that is:
  - PENDING and joinable, meaning automatic: no overrides and no scopes;
  - due now.

  Parked rows and manual or scoped consolidates don't count, because they may not cover the same units
  soon.

  `PendingChecker` finds such a row with a probe transaction that is always rolled back:
  - a `DedupePending` Enqueue of an automatic consolidate joins exactly the joinable pending rows
    (`domain.Operation` exposes neither joinability nor scopes);
  - `Queue.Get(id).NextRunAt` then decides whether the joined row is due.

  If one exists, the runner calls `Fail(cause, nil)`, so the row ends FAILED with its error and one
  OPERATION_FAILED event. The pending job covers the same units, because they are still unstamped. If
  the probe fails, the retry goes ahead.

## Alternatives
- End a skipped consolidate as COMPLETED or CANCELLED. Rejected: it did fail, and recording it any other
  way hides a real gateway or database problem.
- Exempt `ErrRetryAt` from the cap. Rejected: the port can't reschedule without incrementing the attempt,
  and an unbounded self-reschedule loop on a capped kind is worse than failing visibly.

## Spec lines involved
SPEC "Jobs" (Retries), "Behavioural rules" (Operations: "Deterministic failures skip retries"), decision
012 R10'.
