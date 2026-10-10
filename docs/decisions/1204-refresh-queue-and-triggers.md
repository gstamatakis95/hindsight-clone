# 1204 — Refresh queue semantics, triggers and cron

## Context
SPEC: one queued refresh per model (serial key `mm:{id}`); a new request joins it and upgrades it
(`force_full` OR-ed, overrides and strategy replaced) and gets `joined_queued=true`; debounce and the
interval floor park a triggered refresh and later triggers join it; manual refreshes ignore the floor and
release a parked one. `worker.CronRefresher` asks for due-ness keyed on the fire time.

## Decision
- **Payload** `RefreshPayload{mental_model_id, force_full?, strategy?, manual?}`; optional keys are
  omitted when unset so `Queue.Upgrade` keeps the queued row's values (force_full is OR-ed by the queue).
- **EnqueueRefresh(tx, req, now)**: `DedupePending` enqueue (the caller takes `tx.Seq` first). On a join it
  upgrades when the joiner carries force_full, manual, a strategy or overrides; a manual joiner also
  `Reschedule`s the row to now. An automatic joiner keeps the queued `run_at` (bursts join one refresh).
  A refresh requested while one is running queues a new one behind it (the serial key prevents overlap).
- **RefreshScheduler.Decide**: skip when paused or not stale; otherwise due at
  `max(now + refresh_debounce_seconds, last_refreshed_at + min_refresh_interval_seconds)` (trigger value
  over bank value; debounce default 30 s, floor default 0); due now = run now, else park.
- **Triggers**: `AfterConsolidation(bank)` (models with `refresh_after_consolidation`), `Retracted(bank,
  ids)` (models citing the ids) and `EnqueueDueCronRefreshes(now)`, each refresh in its own short tx.
  Consolidation (WP-10) and curation (WP-13) call these, or `EnqueueRefresh` inside their own transaction.
- **Cron**: 5-field UTC expressions (robfig/cron, validated on create/update). A model is due when the
  first fire time after its newest refresh operation's `created_at` (or the model's creation) is at or
  before now; it then goes through `Decide` (so a fresh model is skipped and re-checked next tick). Two
  workers ticking at once join one pending row; a refresh already claimed in between can be enqueued once
  more, which costs at most one NO_NEW_FACTS run with no LLM call (accepted).
- **First build**: CreateMentalModel enqueues a manual, force_full refresh in the create transaction.

## Spec lines involved
Behavioural rules (queued refresh, interval floor), Jobs (refresh_mental_model row, debounce and parking,
maintenance tick), decision 012 (refresh always dedupes as Pending), `worker.CronRefresher`.
