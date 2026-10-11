// Package worker runs background jobs from the Postgres operations table: the job [Runner], the job
// [Handlers] that wrap the engines, the maintenance tick ([RunMaintenance]) and operation cancel/retry
// ([OperationControl]).
//
// # Where it sits
//
// worker sits beside the server, above the engines: it imports domain, store, clock, config and the engines
// whose jobs it runs (retain, consolidate, mentalmodel, bank, curate, docs, maintenance), never an adapter and
// never a tracing vendor. Engines never import worker; they declare what they need from it (retain.Checkpoint,
// consolidate.Checkpointer) and worker's types satisfy those. The tick's cross-bank scans come from
// store.Maintenance, which the Postgres store implements. In its worker
// and all modes cmd/memd runs [Runner.Run] and [RunMaintenance]; the api side uses [OperationControl] for
// CancelOperation and RetryOperation.
//
// # Runner
//
// [NewRunner](claimer, [RunnerConfig]) builds the runner; [Register] puts every handler on it and
// [Runner.Run] blocks until its context ends:
//
//	r := worker.NewRunner(claimer, worker.RunnerConfig{WorkerID: id, TraceJob: traceJob})
//	worker.Register(r, worker.Handlers{Retainer: ..., Consolidator: ..., Store: store, ...})
//	go worker.RunMaintenance(ctx, worker.MaintenanceDeps{Claimer: claimer, Store: store, ...})
//	err := r.Run(ctx)
//
// Every PollInterval (default 500 ms, and after each finished job) the runner claims due jobs through
// store.Claimer (FOR UPDATE SKIP LOCKED in Postgres; one PROCESSING job per serial key, so jobs of one
// document, model or bank never overlap). It runs [DefaultSlots] (10) jobs at once, [DefaultReservedConsolidate]
// (2) of them reserved for consolidate: consolidate may use any free slot, other kinds at most Slots minus the
// reserve. Each job runs in its own goroutine under a context only the runner cancels; a panic becomes a
// retryable error. A handler records its result summary and usage with [ReportResult]; the runner stores
// them with Claimer.Complete when the handler returns nil.
//
// # Retries and failures
//
// A failed run is settled with Claimer.Fail:
//
//   - [ErrPermanent], domain.ErrModelUnbound, domain.ErrInvalidArgument and domain.ErrCancelled never retry.
//   - Every kind except consolidate has 3 attempts (domain.DefaultMaxAttempts) and retries after
//     [RetryStep] x attempt (60 s, 120 s); at the cap it fails for good.
//   - consolidate retries without a cap, with exponential backoff from 60 s capped at
//     [ConsolidateBackoffCap] (30 min). A failed automatic consolidate skips its retry when another
//     automatic consolidate of the bank is already due ([PendingChecker]); a manual one always retries.
//   - delete_bank re-enqueues itself with backoff before failing its last attempt, so a half-deleted bank is
//     never abandoned; graph_maintenance and delete_bank re-enqueue themselves (DedupePending) while their
//     bounded runs report work left; consolidate's engine enqueues its own continuation.
//
// Terminal Complete and Fail calls are retried with backoff (50 ms to 2 s) within 30 s, except a Fail whose
// retry time is within those 30 s, which is tried once: if its ack was lost the row may already be re-claimed
// elsewhere, and repeating it could reset that run. A settle that cannot be written leaves the row PROCESSING
// without heartbeats, and the tick's reclaim re-queues it.
//
// # Heartbeats, fencing and shutdown
//
// Every HeartbeatInterval (default 15 s) the runner heartbeats all its running jobs in one call. A job the
// Claimer reports lost (cancel requested, reclaimed) has its handler cancelled; so has every run whose last
// successful heartbeat is older than 3/4 of StaleAfter (default 2 min), because another worker may reclaim
// it at StaleAfter. A lost run is settled only if Claimer.Alive says no other run holds the row: it is then
// completed (a cancel-requested row ends CANCELLED) or failed with no retry; it is never re-queued, so a job
// never gets two concurrent runs. Re-claiming an id this runner still runs cancels the older run.
//
// When Run's context ends the runner stops claiming, lets handlers finish for ShutdownGrace (default 20 s),
// cancels the rest and releases their rows with Fail at now (which charges an attempt), heartbeating until
// the last handler has been settled. A shutdown can take ShutdownGrace + 30 s, so the pod's termination grace
// period must be at least that (60 s recommended).
//
// # Handlers
//
// [Handlers] are thin wrappers; each engine reads its own payload and re-reads its state from Postgres, so
// a job that runs twice after a reclaim repeats no committed work. A nil engine fails its jobs permanently.
// Registered kinds: retain_document (retain.Retainer), consolidate (consolidate.Consolidator),
// refresh_mental_model (mentalmodel.RunJob), delete_bank (bank.BankDeleter), rederive_memory
// (curate.Rederiver), retag_document (docs.Retagger.Apply) and graph_maintenance
// (maintenance.GraphMaintainer, budget [DefaultGraphBudget] 240 s, with a final re-check of the graph queues).
// retain_batch is never registered: it is a parent row the tick settles. refresh, rederive and retag report one
// progress checkpoint before their engine runs, so a cancelled job or one whose bank is being deleted stops
// before any model call; retain and consolidate report progress through their own [Checkpoint].
// [NewCheckpoint] returns domain.ErrCancelled when the context is done, the row is no longer processing, or
// Claimer.Alive is false (cancel requested, bank tombstoned or gone).
//
// # Maintenance tick
//
// [RunMaintenance] ticks once at start and then every Interval (default 60 s) in every worker process; there
// is no leader, so every step is idempotent and a failing step is logged without stopping the others. The
// steps, in order:
//
//  1. reclaim_stale: Claimer.ReclaimStale(StaleAfter) re-queues PROCESSING rows with an old heartbeat, or
//     fails them past their attempt cap.
//  2. resume_bank_deletes: re-enqueues delete_bank for tombstoned banks with none pending or running.
//  3. refresh_failures: mentalmodel's reconciler pauses models whose latest refresh failed for good.
//  4. cron_refresh: enqueues mental-model refreshes whose refresh_cron fired.
//  5. consolidation_reconcile: enqueues the automatic consolidate for banks with a fact left unconsolidated
//     longer than ConsolidationGrace (default 10 min, so skip_consolidation followed by a manual Consolidate
//     is not pre-empted), unless the bank turns auto_consolidation or enable_observations off.
//  6. graph_reconcile: enqueues graph_maintenance for banks with graph_dirty or entity_maintenance rows.
//  7. rederive_stranded: re-enqueues rederive_memory for facts whose rederive ended FAILED or CANCELLED and
//     left them without an embedding, StrandedRetryAfter (default 10 min) after it ended.
//  8. settle_retain_batches: ends retain_batch parents whose children are all terminal.
//  9. prune_operations / prune_events: delete terminal operations older than OperationRetention (7 days)
//     and lifecycle events older than EventRetention (30 days).
//
// Steps 2, 5 and 6 enqueue the bank's automatic job with payload {}, no overrides and DedupePendingOrRunning,
// one short transaction per bank, so concurrent ticks insert at most one row; a bank being deleted or gone is
// skipped. Step 7 enqueues rederive_memory per fact (curate's payload and serial key, DedupeNone; two workers
// ticking at once may queue a fact twice, which is harmless because the rederive is idempotent). Steps 2, 5,
// 6 and 7 take no tx.Seq: nothing they write is memory content, and Seq refuses a tombstoned bank, which the
// delete resume must still reach. Step 4 goes through mentalmodel's triggers, which take tx.Seq before
// joining or inserting the model's refresh.
//
// # Operation control
//
// [OperationControl.Cancel] marks a pending operation CANCELLED at once and records the request on a
// processing one, which ends CANCELLED when its handler stops; a delete_bank cannot be cancelled
// (FAILED_PRECONDITION). [OperationControl.Retry] re-queues a FAILED or CANCELLED operation (attempt reset);
// on a bank being deleted only delete_bank may be retried.
//
// # Tracing
//
// [RunnerConfig].TraceJob and [MaintenanceDeps].TraceTick are optional hooks ([JobTraceFunc],
// [TickTraceFunc]); cmd/memd sets them from internal/observability when MEMD_DATADOG_ENABLED is on. The job
// span covers the handler and its terminal Complete/Fail and is finished with one of the Outcome* values
// (completed, retry, failed, released, lost); the tick span covers all steps. With the hooks nil there is no
// tracing and no extra work.
//
// # Tests
//
// runner_test.go, exec_retry_test.go and handlers_test.go run against the Postgres Claimer on a pgtest database
// (ops_test.go records Fail calls and ages heartbeats with SQL): slot
// reservation, retry policy and caps, lost-job fencing, shutdown release, settle retries, progress
// checkpoints and handler wiring. maintenance_test.go and maintenance_reconcile_test.go cover each tick step;
// control_test.go covers cancel and retry. SPEC "Jobs", "Process model" and "Behavioural rules" (Operations).
package worker
