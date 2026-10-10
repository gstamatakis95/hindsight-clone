// Package worker runs background jobs from the Postgres operations table. A Runner claims due jobs
// through port.Claimer (FOR UPDATE SKIP LOCKED, one PROCESSING job per serial key), dispatches each to
// the handler registered for its kind, heartbeats running jobs and completes, retries or fails them.
// Handlers are thin wrappers around engines; they report progress and learn about cancellation through
// a Checkpoint. OperationControl implements CancelOperation and RetryOperation, and the maintenance
// tick (every worker, no leader) reclaims stale jobs, enqueues cron refreshes and reconciles
// consolidation.
//
// Only the types are declared so far; WP-05 adds the runtime.
package worker
