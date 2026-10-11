# 1201 — Refresh failures: the runner writes nothing, the reconcile pauses and records

## Context
SPEC 2003: "a failed refresh (after job retries) sets auto_refresh_paused ... Failures are written to
history as HISTORY_KIND_REFRESH_FAILED". The runner can't tell whether an attempt is the last one (the
worker owns retries and the attempt cap), and a reclaimed job past its cap never reaches the handler.
Decision 012 R10' moved pausing into an idempotent RunMaintenance reconcile.

## Decision
- `RefreshRunner.Run` writes nothing on failure: it returns the error and a trace with outcome FAILED;
  the document, version and watermark stay. A retried attempt is therefore never recorded as a failure.
- `RefreshFailureReconciler.ReconcileFailedRefreshes` reads, per model, the latest terminal refresh
  operation when it is FAILED (CCR WP-12-1 `FailedRefreshes`). In one short transaction per model it skips
  the failure when `failed_at < created_at` (the operation belonged to an earlier model with the same id),
  `last_refreshed_at >= failed_at` (a later refresh completed) or `last_refresh_failed_at >= failed_at`
  (already recorded). The check runs once without the lock (a skip takes no `tx.Seq`, so a paused model
  doesn't bump the bank seq every tick), then again after `tx.Seq`, which locks the bank row and so
  serializes the reconcile with the runner's write and Update/Clear on the same model row (two concurrent
  reconciles record a failure once; a refresh write never drops the pause). Otherwise it sets `auto_refresh_paused`,
  `last_refresh_failed_at = failed_at` (the idempotence key, so no `operation_id` column is needed),
  appends one REFRESH_FAILED entry (capped by `history_max_entries`) and a MENTAL_MODEL_REFRESH_FAILED
  event stamped with `tx.Seq`.
- "Latest terminal refresh operation" means the latest COMPLETED or FAILED one; a CANCELLED operation (e.g. a
  job cancelled by a bank delete or a superseded join) neither records nor hides a failure. The Postgres
  `FailedRefreshes` read is WP-15's; it must filter on those two states.
- `failure_reason` is classified from the operation's error text: `delta_ops_all_skipped`,
  `empty_candidate`, `structured_output_failed`, `retrieval_failed` (anything wrapping
  `domain.ErrUnavailable`), else `unexpected_error`.
- Automatic triggers (scheduler, cron, after-consolidation, retraction) skip a paused model. Only a
  successful run with `Manual` set (RefreshMentalModel, the first build) clears the flag; a NO_NEW_FACTS
  manual run counts as a success.
- `RunJob` treats a model or bank deleted before the job ran as done (nil), so a stale job is harmless.

## Alternatives
- Recording failures in the runner on the last attempt: the runner would need the attempt cap and would
  still miss reclaim-failed jobs. Rejected.
- A `mental_model_history.operation_id` column for exact idempotence: a frozen-schema change; the
  failure time key is enough.

## Spec lines involved
Behavioural rules (Paused by failure), Decisions (Paused auto-refresh), decision 012 R10',
`worker.RefreshFailureReconciler`.
