# 2000 — The maintenance tick consolidates only banks that want it, and only after a grace period

## Context
SPEC "Jobs" (maintenance tick) says to enqueue consolidation for banks with unconsolidated units and none
pending. SPEC "Overrides" (line 908) says "Use `skip_consolidation` and a manual Consolidate call when a batch
needs its own settings". Final review B (H3, plus the enable_observations MEDIUM) found that the tick
consolidated every such bank:
- It ignored `auto_consolidation=false`.
- On `enable_observations=false` banks it queued one no-op consolidate a minute, forever. The run returns
  early without stamping anything.
- It consolidated `skip_consolidation` facts with bank defaults within 60 s, so a later manual Consolidate
  with overrides found nothing to do.

The orchestrator ruled: skip those banks, and reconcile only units older than a grace period (default
10 min, a config knob).

## Decision
- **The read.** A new Postgres store method, `(*postgres.Store).UnconsolidatedBanks(ctx, before)`. It is
  BanksToConsolidate plus `u.created_at < before`, over the same partial index
  `memory_units_unconsolidated_idx (tenant_id, bank_id, created_at)`. It is not a port method. The worker
  consumes it through `worker.ConsolidationCandidates`, the pattern of decisions 1452 and 1453.
  `port.Reader.BanksToConsolidate` stays as it is, and nothing calls it any more.
- **The cut-off** is `Clock.Now() - ConsolidationGrace`, taken from the injected clock.
- **The config filter.** `MaintenanceDeps.Config` (a port.ConfigResolver) resolves each candidate bank's
  effective config with no overrides. The bank is skipped when `auto_consolidation` or `enable_observations`
  is false. A bank whose config lookup says NOT_FOUND or deleting is skipped silently. A nil resolver
  filters nothing.
- **The knob.** `MEMD_CONSOLIDATION_RECONCILE_GRACE` (flag `-consolidation-reconcile-grace`), default
  `10m`, on `MaintenanceDeps.ConsolidationGrace`. It is a process setting, not a BankConfig field.
  - AGENTS.md says new behaviour knobs go in BankConfig and must be overridable per request.
  - That rule doesn't fit here. BankConfig is a frozen domain type, and changing it would need a CCR.
  - The tick has no request to override from.
  - The grace is an operational timing, like the tick interval and the stale-after threshold, which are
    process settings too.
- `created_at` is the unit's insert time. Units reset by retag, clears or invalidate keep their old
  `created_at`, so the tick reconciles them at once. Those paths enqueue their own consolidation anyway.

## Consequences
- A `skip_consolidation` retain followed by a manual Consolidate within 10 minutes is applied with the
  caller's overrides. If no manual call comes, the tick consolidates the facts with bank defaults after
  10 minutes, as before.
- `auto_consolidation=false` banks are consolidated only by explicit Consolidate calls and by the curation
  paths that enqueue consolidation directly (edit, invalidate, retag).

## Alternatives
- Stamping `skip_consolidation` units with a marker. Rejected: it needs a schema change.
- Making the consolidate run stamp the units it skips when observations are disabled. Rejected: it would
  lose those facts if observations were turned back on later.

## Spec lines involved
SPEC "Jobs" (maintenance tick), "Overrides" (skip_consolidation + manual Consolidate), BankConfig
`auto_consolidation` and `enable_observations`; decisions 502, 1452, 1453 and 1500.
