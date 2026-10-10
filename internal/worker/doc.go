// Package worker runs background jobs from the Postgres operations table. A Runner claims due jobs
// through port.Claimer (FOR UPDATE SKIP LOCKED, one PROCESSING job per serial key), dispatches each to
// the handler registered for its kind, heartbeats running jobs and completes, retries or fails them.
// It runs 10 jobs at once by default, 2 slots reserved for consolidate; retries follow the per-kind
// policy (60 s × attempt up to 3 attempts; consolidate forever with capped exponential backoff).
// Handlers are thin wrappers around engines; they report progress and learn about cancellation through
// a Checkpoint, and hand their result to Complete with ReportResult. OperationControl implements
// CancelOperation and RetryOperation, and the maintenance tick (every worker, no leader) reclaims stale
// jobs, reconciles failed refreshes, enqueues cron refreshes, reconciles consolidation and prunes old
// operations and events.
package worker
