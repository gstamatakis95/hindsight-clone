// Package jobs runs background jobs from the Postgres operations table and owns the job catalogue: the
// [Runner] that claims and settles jobs, the [Run] handle a handler works through, the retry policy, and
// kinds.go, which holds every job kind's payload, serial key and Enqueue helper.
//
// # Where it sits
//
// jobs imports domain, store and clock only. The [Claimer] interface is its boundary to the operations
// table; the Postgres store implements it. Engines import jobs for the catalogue and for [Run]; cmd/memd
// registers one [Handler] per kind and runs [Runner.Run] in its worker and all modes. The api side uses
// [OperationControl] for CancelOperation and RetryOperation.
//
// # Catalogue
//
// Every enqueue goes through a helper in kinds.go, so a kind's payload bytes, serial key and dedupe policy
// are decided once:
//
//   - retain_document: [EnqueueRetainDocument] ("doc:{id}", DedupeNone; under a retain_batch parent from
//     [EnqueueRetainBatch], which has no serial key and is never run);
//   - consolidate: [EnqueueConsolidate] (payload {}, DedupePending), [EnqueueAutoConsolidate] (the tick:
//     DedupePendingOrRunning) and [EnqueueScopedConsolidate] (scopes or overrides: DedupeNone);
//   - refresh_mental_model: [EnqueueRefresh] (join, upgrade, release) and [QueueModelRefresh] (paused skip,
//     first build in full, else parked until domain.RefreshDueAt);
//   - graph_maintenance: [EnqueueGraph] (DedupePendingOrRunning);
//   - delete_bank: [EnqueueDeleteBank] (DedupePendingOrRunning) and [EnqueueDeleteBankContinuation];
//   - rederive_memory: [EnqueueRederive] ([RederiveSerialKey], DedupeNone);
//   - retag_document: [EnqueueRetag] ("doc:{id}", DedupePending).
//
// No helper calls tx.Seq: a transaction that changes memory content calls tx.Seq(bank) first, before any
// read it depends on and before the enqueue. [Requeue] enqueues a bounded run's continuation (DedupePending)
// in its own transaction.
//
// # Runner
//
// [New](claimer, [Config]) builds the runner; [Runner.Handle] registers a [Handler] per kind and
// [Runner.Run] blocks until its context ends:
//
//	r := jobs.New(claimer, jobs.Config{WorkerID: id, DB: db, TraceJob: traceJob})
//	r.Handle(domain.OperationKindConsolidate, func(ctx context.Context, run *jobs.Run) error { ... })
//	err := r.Run(ctx)
//
// Every PollInterval (default 500 ms, and after each finished job) the runner claims due jobs through the
// Claimer (FOR UPDATE SKIP LOCKED in Postgres; one PROCESSING job per serial key, so jobs of one document,
// model or bank never overlap). It runs [DefaultSlots] (10) jobs at once, [DefaultReservedConsolidate] (2) of
// them reserved for consolidate: consolidate may use any free slot, other kinds at most Slots minus the
// reserve. Each job runs in its own goroutine under a context only the runner cancels; a panic becomes a
// retryable error.
//
// # Run
//
// A handler gets a [Run]: Run.Job is the claimed row, [Run.Report] writes progress and returns
// domain.ErrCancelled once the context is done, the row is no longer processing, or the job is no longer
// alive (cancel requested, bank tombstoned or gone); [Run.Alive] asks without writing; [Run.SetResult]
// records the result summary and usage, which the runner stores with Claimer.Complete when the handler
// returns nil. [Inline] is the run of a sync retain that no worker claimed: it reports nothing and is always
// alive.
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
//     automatic consolidate of the bank is already due (a probe through Config.DB); a manual one always
//     retries.
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
// successful heartbeat is older than 3/4 of StaleAfter (default [DefaultStaleAfter], 2 min), because another
// worker may reclaim it at StaleAfter. A lost run is settled only if Claimer.Alive says no other run holds
// the row: it is then completed (a cancel-requested row ends CANCELLED) or failed with no retry; it is never
// re-queued, so a job never gets two concurrent runs. Re-claiming an id this runner still runs cancels the
// older run.
//
// When Run's context ends the runner stops claiming, lets handlers finish for ShutdownGrace (default 20 s),
// cancels the rest and releases their rows with Fail at now (which charges an attempt), heartbeating until
// the last handler has been settled. A shutdown can take ShutdownGrace + 30 s, so the pod's termination grace
// period must be at least that (60 s recommended).
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
// Config.TraceJob is an optional hook ([TraceFunc]); cmd/memd sets it from internal/observability when
// MEMD_DATADOG_ENABLED is on. The job span covers the handler and its terminal Complete/Fail and is finished
// with one of the Outcome* values (completed, retry, failed, released, lost). With the hook nil there is no
// tracing and no extra work.
//
// # Tests
//
// The runner tests run against the Postgres Claimer on a pgtest database (ops_test.go records Fail calls and
// ages heartbeats with SQL): slot reservation, retry policy and caps, lost-job fencing, shutdown release,
// settle retries, progress checkpoints and results. kinds_test.go pins every helper's row. SPEC "Jobs",
// "Process model" and "Behavioural rules" (Operations).
package jobs
