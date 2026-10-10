# 1304 — Graph maintenance: batches, budget, termination and the remaining enqueue window

## Context
SPEC "Jobs": graph_maintenance (serial key `graph`, dedupe pending or running) drains `graph_dirty` and
`entity_maintenance` within 240 s and re-enqueues if work is left; "Behavioural rules": it refills
temporal and semantic links of neighbours that fell below their caps and prunes entities and co-occurrences
no live unit references, in committed batches, resuming where it stopped. Hindsight's
`graph_maintenance.py` does the same with a wall-clock deadline and a hand-off to a successor job.

## Decision
- **Batches.** One transaction per batch: up to `DirtyBatch` (100) units from `LinkRepo.Dirty`, re-read
  with `GetUnits`, passed to the Linker (which fills up to `temporal_link_cap` / `semantic_link_k` and
  keeps existing edges), then `ClearDirty` for every taken id even when the Linker added nothing; plus up
  to `EntityBatch` (500) ids from `PendingMaintenance` passed to `PruneOrphans` (which removes every
  examined id from the queue). Every step is idempotent, so a run repeated after a reclaim only redoes
  uncleared rows.
- **Budget.** `deadline = clock.Now() + budget` on the injected `port.Clock`, checked between batches
  only (a batch is never cut in half). `DefaultGraphBudget` = 240 s; the handler passes it. Running out
  with work done in the last batch returns `done=false`; the handler re-enqueues with `DedupePending`
  (its own row is PROCESSING, so this inserts the successor, which the serial key holds back until this
  run completes).
- **Termination.** `done=true` only when a batch finds both queues empty; that batch is the final
  re-check and always runs after the last batch that did work, so anything queued while a working batch ran
  (even inside its own transaction) is drained before the run ends (`TestGraphMaintenanceRechecksAfterFinalBatch`).
  Queue emptiness is decided by the queues themselves, never by a count of work done.
- **Triggers.** Every path that removes units or detaches entities enqueues graph_maintenance
  (`DedupePendingOrRunning`); in the docs package that is the retag job when it deletes observations
  (review round 1, item 4).
- **Cancellation.** A batch error after ctx ended is reported as `domain.ErrCancelled`.
- **No change seq.** Links, entities and co-occurrences are not memory content: no `tx.Seq`, so a
  maintenance run never makes mental models stale.
- **Deleting banks.** Each batch first reads the bank row; a tombstoned or gone bank stops the run with
  `domain.ErrCancelled` (claims already skip deleting banks; this covers a run in flight). The maintainer
  never calls `Banks().Ensure`.

## Known gap (accepted)
The only remaining window is between the final empty check and the handler's `Complete`: a removal that
commits there enqueues
graph_maintenance with `DedupePendingOrRunning`, joins this still-PROCESSING row and is dropped. Its
`graph_dirty` / `entity_maintenance` rows are not lost: the next removal (or any later trigger) drains
them. Hindsight has the same window (it re-checks after the run, gated on progress). Closing it fully
would need the handler to re-enqueue unconditionally after a run that made progress (one extra no-op run
per maintenance); left to WP-14 if wanted.

## Alternatives
- A count-based stop ("processed fewer than a batch"): rejected, it can end with rows still queued.
- Taking `tx.Seq` per batch to fence tombstoned banks: rejected, it bumps `change_seq` for non-content
  writes; an explicit bank check is enough.

## Spec lines involved
SPEC "Jobs" (graph_maintenance row, bounded runs), "Behavioural rules" (banks and documents, graph
maintenance), Hindsight `graph_maintenance.py`.
