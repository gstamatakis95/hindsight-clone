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
- **Re-park inside the floor** (orchestrator ruling): an automatic run that starts inside the interval
  floor (e.g. a trigger that bypassed `Decide`) enqueues a refresh of the model with
  `run_at = max(last_refreshed_at + floor, now + 5 s)` (`DedupePending`, so it joins a pending one; the
  5 s and the matching "more than 5 s left" floored check keep DB / worker clock skew from looping; the running job is
  PROCESSING, so otherwise a new row is inserted behind it), then finishes as NO_NEW_FACTS without
  writing. A quiet bank therefore can't leave the model stale forever.
- **Triggers**: `AfterConsolidation(bank)` (models with `refresh_after_consolidation`), `Retracted(bank,
  ids)` (models citing the ids) and `EnqueueDueCronRefreshes(now)`, each refresh in its own short tx.
  Consolidation (WP-10) and curation (WP-13) call these, or `EnqueueRefresh` inside their own transaction.
- **Cron**: exactly 5 fields (minute hour dom month dow) in UTC, parsed with robfig/cron and validated
  on create/update; `@` descriptors, a `TZ=`/`CRON_TZ=` prefix and a seconds field are rejected. Due-ness
  is keyed on the fire time with a grace window (`cronGrace` = 5 min; maintenance ticks every 60 s): let
  F be the latest fire time in `(now - 5 min, now]`. A model is due iff F exists, its newest refresh
  operation's `created_at` is before F (none counts as before), and F is not before the model's creation.
  A fire time missed by more than the grace window (service down, ticks stalled) is skipped until the next
  fire time, so a cron refresh never runs off-schedule at a random later tick (accepted trade-off). A due
  model then goes through `Decide` (paused or fresh models are skipped and re-checked on the next tick,
  within the window). Two workers ticking at once join one pending row; a refresh already claimed in
  between can be enqueued once more, which costs at most one NO_NEW_FACTS run with no LLM call (accepted).
- **RefreshQueuer** (for WP-13 decision 1303, `bank.RefreshQueuer` / `curate.RefreshQueuer`):
  `QueueRefresh(ctx, tx, b, id)` runs inside the caller's transaction, so it reads only through `tx` (no
  ConfigResolver, which opens its own transaction, and no change detector). A paused model is skipped
  (returns ""); a never-built model is due now and runs in full (template first builds); otherwise the run is
  parked for the debounce / interval floor taken from the model's overrides, then the bank's own config,
  then the defaults. Tenant-level mental-model knobs are not seen on this path (accepted).
- **First build**: CreateMentalModel enqueues a manual, force_full refresh in the create transaction.

## Spec lines involved
Behavioural rules (queued refresh, interval floor), Jobs (refresh_mental_model row, debounce and parking,
maintenance tick), decision 012 (refresh always dedupes as Pending), `worker.CronRefresher`.
