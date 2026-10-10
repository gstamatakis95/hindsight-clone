# 502 — Maintenance tick: consumer interfaces, order and the consolidate job shape

## Context
`RunMaintenance` must do five things: reclaim stale jobs, enqueue cron refreshes, reconcile
consolidation, prune terminal operations and events, and, as the carried-forward item from decision 012
R10', reconcile failed refreshes into paused models. The ports cover reclaim (`Claimer.ReclaimStale`)
and pruning (`Queue.Prune`, `EventRepo.Prune`). They do not cover:
- listing banks with unconsolidated units;
- listing models with a cron schedule;
- finding failed refresh operations.

Mental-model payloads and cron semantics belong to WP-12.

## Decision
- **Consumer interfaces** live in the worker package, so no frozen port changes. A nil dependency skips
  its step.
  - `ConsolidationCandidates.BanksToConsolidate(ctx)` returns every live bank with unconsolidated,
    unparked units. The Postgres adapter can implement it on its concrete Store, or a later WP behind a
    CCR'd Reader method; WP-14 wires it.
  - `CronRefresher.EnqueueDueCronRefreshes(ctx, now)` belongs to WP-12. It does its own enqueues, because
    the refresh payload shape is WP-12's. Its doc requires idempotence under concurrent callers, keyed on
    the cron fire time, because refresh dedupe only joins PENDING rows.
  - `RefreshFailureReconciler.ReconcileFailedRefreshes(ctx)` belongs to WP-12. Its doc carries the ruling:
    key on the latest *terminal* refresh operation (FAILED and newer than the last success), and be
    idempotent.
- **The consolidation enqueue stays in the worker.** It runs one short transaction per bank with:
  - kind `consolidate`, serial key `consolidate`;
  - payload `{}`, no overrides, no scopes;
  - `DedupePendingOrRunning`. It joins the bank's queued *or running* automatic consolidate, because a
    running consolidate re-enqueues itself while units remain. Joining a running one also avoids a
    trap: with `DedupePending`, a tick during a slow, failing run would queue a fresh job, so the
    failing run would skip its retry (decision 500) and end FAILED. Backoff would reset on every
    failure and each failure would emit an event. Jobs enqueued by retain, rederive and similar paths
    keep the spec's `DedupePending`. So the trap still exists for those: a retain that commits during
    a failing consolidate queues a due automatic consolidate, and the failing run then ends FAILED
    instead of retrying. That is the spec's own rule ("skipping the retry if another consolidation is
    already pending"). No work is lost, because the queued job picks up the same units.

  Concurrent ticks therefore insert at most one job per bank, and that is what the acceptance test
  checks. `ErrBankDeleting` and `ErrNotFound` skip the bank. Any other error is collected without
  stopping the other banks. WP-10's handler must accept the empty payload as "all scopes, bank
  defaults".
- **Order.** The steps run in this order:
  1. reclaim;
  2. refresh-failure reconcile (it sees refreshes that reclaim just failed);
  3. cron refreshes (they skip paused models);
  4. consolidation reconcile;
  5. prune operations;
  6. prune events.

  A failing step is logged and the rest still run. The tick runs once at start, then every 60 s.
- **Retention.** Defaults are 7 days for terminal operations and 30 days for events, and both can be set
  in `MaintenanceDeps`. Operation retention must stay far above the tick interval, so the failure
  reconcile sees every FAILED refresh before pruning removes it.

## Alternatives
- Have the worker enqueue cron refreshes from a "due models" read. Rejected: it would fix WP-12's payload
  and due-ness rules here.
- Use `DedupePending` for the reconcile, as the spec's table does for consolidate. Rejected for this one
  safety-net enqueue: it interacts badly with the skip-retry rule, as described above.

## Spec lines involved
SPEC "Jobs" (Maintenance tick, table row for consolidate), "Function signatures" (worker), decision 012
R10', STATUS carried-forward item for WP-12/WP-05.
