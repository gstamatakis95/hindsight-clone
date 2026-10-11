// Package maintenance runs the worker's 60 s maintenance tick and the graph_maintenance job. Its one type,
// [Service], is built by [New] from [Deps]; [Service.Run] is the tick loop and [Service.RunGraphJob] the
// graph_maintenance handler.
//
// # Where it sits
//
// maintenance sits above the services and jobs: it calls the services' sweeps (bank.Service's delete resume,
// mentalmodel.Service's cron refreshes and failed-refresh reconcile, observation.Service's consolidation
// reconcile, memory.Service's stranded-fact and retain-batch sweeps), the cross-bank scans of store.Maintenance
// and the jobs catalogue, and never an adapter. No service imports it. cmd/memd/wire_services.go builds it and
// registers [Service.RunGraphJob]; the worker and all modes run [Service.Run] beside the job runner. Its [Config] holds
// the operator settings (tick interval, reclaim age, retention of terminal operations and lifecycle events, graph
// budget); zero values take the defaults.
//
// # Tick
//
// [Service.Run] ticks once at start and then every Interval (default 60 s) in every worker process; there
// is no leader, so every step is idempotent and a failing step is logged without stopping the others. A
// step whose dependency is nil is skipped. The steps, in order:
//
//  1. reclaim_stale: Claimer.ReclaimStale(StaleAfter) re-queues PROCESSING rows with an old heartbeat, or
//     fails them past their attempt cap.
//  2. resume_bank_deletes: bank.Service.ResumeStalledDeletes re-enqueues delete_bank for tombstoned banks with
//     none pending or running.
//  3. refresh_failures: mentalmodel.Service.ReconcileFailedRefreshes pauses models whose latest refresh failed
//     for good.
//  4. cron_refresh: mentalmodel.Service.EnqueueDueCronRefreshes enqueues refreshes whose refresh_cron fired.
//  5. consolidation_reconcile: observation.Service.ReconcileConsolidation enqueues the automatic consolidate
//     for banks with a fact left unconsolidated longer than the bank's observations.reconcile_grace_seconds
//     (default 600, so skip_consolidation followed by a manual Consolidate is not pre-empted), unless the bank
//     turns observations.auto_consolidation or observations.enabled off.
//  6. graph_reconcile: enqueues graph_maintenance for banks with graph_dirty or entity_maintenance rows.
//  7. rederive_stranded: memory.Service.RederiveStranded re-enqueues rederive_memory for facts whose rederive
//     ended FAILED or CANCELLED and left them without an embedding, 10 minutes after it ended.
//  8. settle_retain_batches: memory.Service.SettleRetainBatches ends retain_batch parents whose children are
//     all terminal.
//  9. prune_operations / prune_events: delete terminal operations older than OperationRetention (7 days)
//     and lifecycle events older than EventRetention (30 days).
//
// Steps 2, 5 and 6 enqueue the bank's automatic job (jobs.EnqueueDeleteBank, jobs.EnqueueAutoConsolidate,
// jobs.EnqueueGraph: payload {}, no overrides, DedupePendingOrRunning), one short transaction per bank, so
// concurrent ticks insert at most one row; a bank being deleted or gone is skipped. Step 7 uses
// jobs.EnqueueRederive (DedupeNone; two workers ticking at once may queue a fact twice, which is harmless
// because the rederive is idempotent). Steps 2, 5, 6 and 7 take no tx.Seq: nothing they write is memory
// content, and Seq refuses a tombstoned bank, which the delete resume must still reach. Step 4 takes tx.Seq
// before joining or inserting each model's refresh.
//
// # Graph maintenance
//
// The store methods that remove units (UnitRepo.DeleteByDocument on document replace and reprocess,
// UnitRepo.Archive on curation) fill two queues in the removing transaction:
//
//   - graph_dirty: the removed units' live link neighbours, which may have fallen below their link caps
//     (temporal_link_cap, semantic_link_k).
//   - entity_maintenance: the removed units' entities, whose last referencing unit may be gone.
//
// The services on those paths then enqueue graph_maintenance (jobs.EnqueueGraph), and step 6 of the tick
// enqueues it for every bank with rows in either queue. [Service.RunGraphJob] computes deadline = now +
// GraphBudget (default 240 s) on the injected clock and loops, one DB.InTx per batch:
//
//  1. Read the bank row. A tombstoned or missing bank stops the run with domain.ErrCancelled.
//  2. Take up to [DirtyBatch] (100) ids from graph_dirty, re-read them and pass the live ones to the Linker,
//     which adds the temporal and semantic links they lack up to the per-unit caps. Every taken id is then
//     cleared from graph_dirty.
//  3. Take up to [EntityBatch] (500) ids from entity_maintenance and call PruneOrphans, which deletes the
//     entities (and their co-occurrences) that no live unit references.
//  4. Commit. If the batch consumed nothing from either queue the drain is done. Otherwise, if the deadline
//     has passed, the run re-enqueues itself (jobs.Requeue, DedupePending); else it runs the next batch.
//
// A drain that is done re-checks both queues once more and re-enqueues if a row appeared: a removal that
// committed after the final empty batch joined this running job. Links and entities are not memory content,
// so no batch takes tx.Seq; nothing here calls a model, so a whole batch is safe inside InTx.
//
// # Tests
//
// The tests run against a pgtest database: step order and isolation, concurrent ticks with no duplicate
// enqueues, each reconcile, the prunes, and the graph job (top-up and prune, budget, tombstone, re-check and
// re-enqueue). Design: docs/design/architecture.md#maintenance-tick and
// docs/design/retain.md#graph-maintenance.
package maintenance
