# 2002 — The maintenance tick enqueues graph_maintenance for banks with queued graph work

## Context
Every removal path enqueues graph_maintenance with DedupePendingOrRunning. Suppose a removal commits after
the graph handler's final re-check and before its Complete. It then joins the finishing row, and its
`graph_dirty` and `entity_maintenance` rows wait for the next removal (final review B, LOW).

## Decision
- **The read.** A new Postgres store method, `(*postgres.Store).BanksWithGraphWork(ctx)`, lists the
  non-tombstoned banks with any `graph_dirty` or `entity_maintenance` row. Both lookups are EXISTS probes
  on the primary keys. It is consumed through `worker.GraphWorkSource`.
- **The tick step.** `graph_reconcile` runs after the consolidation reconcile. It enqueues graph_maintenance
  (serial key `graph`, payload `{}`, DedupePendingOrRunning) for each listed bank, so it joins a queued or
  running row and concurrent ticks insert one.
- The handler already clears `graph_dirty` rows of units that are no longer live, so the step can't loop
  on stale rows.

## Spec lines involved
SPEC "Behavioural rules" (graph_maintenance after removals) and "Jobs" (maintenance tick); decision 1451.
