// Package maintenance repairs the memory graph after units are removed or entities detached. Its one
// engine, [GraphMaintainer], is the body of the graph_maintenance job.
//
// # Where it sits
//
// maintenance is an engine package: it depends on domain and the boundary packages only and must never import adapters,
// server, worker, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It declares the small [Linker] it
// consumes instead of importing retain; retain.Linker satisfies it (wire.go fails to compile otherwise), and
// cmd/memd/wire.go passes the retain linker to [NewGraphMaintainer]. The worker's graph handler calls
// [GraphMaintainer.Run].
//
// # Queues it drains
//
// The store methods that remove units (UnitRepo.DeleteByDocument on document replace and reprocess,
// UnitRepo.Archive on curation) fill two queues in the removing transaction:
//
//   - graph_dirty: the removed units' live link neighbours, which may have fallen below their link caps
//     (temporal_link_cap, semantic_link_k).
//   - entity_maintenance: the removed units' entities, whose last referencing unit may be gone.
//
// The engines on those paths (retain, curate, and docs when a retag deletes observations) then enqueue
// graph_maintenance with serial key "graph", payload {} and DedupePendingOrRunning. The worker's
// maintenance tick also enqueues graph_maintenance for every non-deleting bank with rows in
// either queue, so a removal that joined a finishing run is not left waiting.
//
// # Flow of Run
//
// [GraphMaintainer.Run](ctx, bank, budget) computes deadline = clock.Now() + budget on the injected
// clock.Clock and then loops, one DB.InTx per batch:
//
//  1. Read the bank row. A tombstoned or missing bank stops the run with domain.ErrCancelled (a job never
//     writes to a deleting bank; the maintainer never calls Banks().Ensure).
//  2. Take up to [DirtyBatch] (100) ids from graph_dirty, re-read them with GetUnits and pass the live ones
//     to the Linker, which adds the temporal and semantic links they lack up to the per-unit caps and keeps
//     existing edges. Every taken id is then cleared from graph_dirty, whether or not a link was added.
//  3. Take up to [EntityBatch] (500) ids from entity_maintenance and call PruneOrphans, which deletes the
//     entities (and their co-occurrences) that no live unit references and removes every examined id from
//     the queue.
//  4. Commit. If the batch consumed nothing from either queue, return done=true. Otherwise, if the deadline
//     has passed, return done=false; else run the next batch.
//
// The budget is checked between batches only, so a batch is never cut in half. done=true is reported only
// by a batch that found both queues empty after the last batch that did work, so anything queued while a
// working batch ran is drained before the run ends (TestGraphMaintenanceRechecksAfterFinalBatch).
//
// On done=false the worker re-enqueues graph_maintenance with DedupePending; on done=true it re-checks both
// queues once more and re-enqueues if a row appeared. The worker passes
// worker.DefaultGraphBudget (240 s, SPEC "Jobs"); [DefaultGraphBudget] here holds the same value.
//
// # Invariants
//
//   - No tx.Seq. Links, entities and co-occurrences are not memory content, so a maintenance batch never
//     bumps the bank's change seq and never makes a mental model stale.
//   - Idempotent. Every step consumes its queue rows in the same transaction that does the work, so a run
//     repeated after a reclaim only redoes rows that were not yet cleared.
//   - No model calls. Linking reads stored embeddings; nothing here calls an LLM, embedder or reranker, so
//     the whole batch is safe inside InTx. The Postgres InTx re-runs a batch up to 3 times in all after a
//     deadlock (40P01) or serialization failure (40001).
//   - Cancellation. An InTx error after ctx ended is reported as domain.ErrCancelled.
//   - PruneOrphans takes no bank seq, so it can delete an entity that a concurrent retain or curation
//     transaction has just resolved a mention to. Postgres Attach then fails with a conflict and that other
//     transaction re-runs and re-resolves; the pruning side needs no lock.
//
// # Tests
//
// graph_impl_test.go runs the maintainer against a pgtest database with a stub Linker and a
// clock.Manual: top-up and prune, resumption after the budget runs out, the tombstone stop, and the final
// re-check. SPEC "Jobs" (graph_maintenance) and "Behavioural rules" (graph maintenance after removals).
package maintenance
