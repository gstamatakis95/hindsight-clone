-- QueueLockBank takes FOR KEY SHARE on the bank row and returns its tombstone (NULL = live).
-- name: QueueLockBank :one
SELECT deleting_at FROM banks WHERE tenant_id = @tenant_id AND org_id = @org_id AND id = @id FOR KEY SHARE;

-- QueueDedupeLock serializes dedupe enqueues of one (tenant, org, bank, kind, serial key) until the tx ends.
-- name: QueueDedupeLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    @tenant_id::text || '/' || @org_id::text || '/' || @bank_id::text || '/' || @kind::text || '/'
    || @serial_key::text, 0));

-- QueueFindJoinable locks and returns the oldest joinable row an enqueue may join.
-- name: QueueFindJoinable :one
SELECT id FROM operations
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND kind = @kind AND serial_key = @serial_key::text
  AND joinable
  AND (status = 'pending' OR (@include_running::bool AND status = 'processing'))
ORDER BY created_at, id
LIMIT 1
FOR UPDATE;

-- QueueInsert inserts one pending job; a NULL run_at means the database's now().
-- name: QueueInsert :exec
INSERT INTO operations (
    id, tenant_id, org_id, bank_id, kind, serial_key, payload, overrides, joinable, parent_id, attempts, run_at,
    created_at, updated_at
) VALUES (
    @id, @tenant_id, @org_id, @bank_id, @kind, @serial_key, @payload, @overrides, @joinable, @parent_id, @attempts,
    COALESCE(sqlc.narg(run_at)::timestamptz, now()), clock_timestamp(), clock_timestamp()
);

-- QueueLockOp locks one operation row and returns it.
-- name: QueueLockOp :one
SELECT * FROM operations WHERE id = $1 FOR UPDATE;

-- QueueSetPayload replaces a pending job's payload and overrides.
-- name: QueueSetPayload :exec
UPDATE operations SET payload = @payload, overrides = @overrides, updated_at = now() WHERE id = @id;

-- QueueReschedule sets a pending job's run_at.
-- name: QueueReschedule :execrows
UPDATE operations SET run_at = @run_at, updated_at = now() WHERE id = @id AND status = 'pending';

-- QueueStatus returns an operation's status.
-- name: QueueStatus :one
SELECT status FROM operations WHERE id = $1;

-- QueueCancelBank cancels every pending job of a bank except its delete_bank job.
-- name: QueueCancelBank :execrows
UPDATE operations
SET status = 'cancelled', finished_at = now(), joinable = false, updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND status = 'pending'
  AND kind <> 'delete_bank';

-- QueueCancelOrg cancels every pending job of an org, its banks' included, except delete_bank and delete_org.
-- name: QueueCancelOrg :execrows
UPDATE operations
SET status = 'cancelled', finished_at = now(), joinable = false, updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND status = 'pending'
  AND kind NOT IN ('delete_bank', 'delete_org');

-- QueueProcessingInOrg reports whether an operation of an org other than except_id is processing.
-- name: QueueProcessingInOrg :one
SELECT EXISTS (
    SELECT 1 FROM operations
    WHERE tenant_id = @tenant_id AND org_id = @org_id AND status = 'processing' AND id <> @except_id::uuid
)::bool AS busy;

-- QueueGet returns one operation of a bank (of the org itself when bank_id is '').
-- name: QueueGet :one
SELECT * FROM operations WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id;

-- QueueList pages a bank's operations newest first, after an optional (created_at, id) cursor.
-- name: QueueList :many
SELECT * FROM operations
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (NOT @exclude_parents::bool OR kind <> 'retain_batch')
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, @after_id::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- QueueCancel cancels a pending job at once or records the cancel request of a processing one.
-- name: QueueCancel :one
UPDATE operations
SET status = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
    finished_at = CASE WHEN status = 'pending' THEN now() ELSE finished_at END,
    cancel_requested_at = CASE WHEN status = 'processing' THEN COALESCE(cancel_requested_at, now())
                               ELSE cancel_requested_at END,
    joinable = false, updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id
  AND status IN ('pending', 'processing')
RETURNING *;

-- QueueRetry re-queues a failed or cancelled job as a fresh pending row due now.
-- name: QueueRetry :one
UPDATE operations
SET status = 'pending', run_at = now(), attempts = 0, error = NULL, finished_at = NULL,
    cancel_requested_at = NULL, worker_id = NULL, heartbeat_at = NULL,
    joinable = joinable OR kind = 'refresh_mental_model', updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id
  AND status IN ('failed', 'cancelled')
RETURNING *;

-- ClaimFailUndecodable ends a selected pending row whose stored overrides can't be decoded FAILED at once, so it
-- can't block its serial key or be claimed again.
-- name: ClaimFailUndecodable :exec
UPDATE operations SET status = 'failed', error = @error, finished_at = now(), joinable = false, updated_at = now()
WHERE id = @id AND status = 'pending';

-- QueuePrune deletes terminal operations finished before the cutoff.
-- name: QueuePrune :execrows
DELETE FROM operations
WHERE status IN ('completed', 'failed', 'cancelled') AND finished_at < @before::timestamptz;

-- ClaimSelect is the claim query: due jobs of the given kinds whose serial key is free, oldest first. A bank job
-- needs its bank row, live unless the job is delete_bank; an org-scoped job (bank_id '') needs only its org row.
-- A tombstoned org runs only delete_bank and delete_org.
-- name: ClaimSelect :many
SELECT o.* FROM operations o
JOIN orgs g ON (g.tenant_id, g.id) = (o.tenant_id, o.org_id)
LEFT JOIN banks b ON (b.tenant_id, b.org_id, b.id) = (o.tenant_id, o.org_id, o.bank_id)
WHERE o.status = 'pending' AND o.run_at <= now() AND o.kind = ANY($1::text[])
  AND (g.deleting_at IS NULL OR o.kind IN ('delete_bank', 'delete_org'))
  AND (o.bank_id = '' OR (b.id IS NOT NULL AND (b.deleting_at IS NULL OR o.kind = 'delete_bank')))
  AND NOT EXISTS (
    SELECT 1 FROM operations p
    WHERE (p.tenant_id, p.org_id, p.bank_id, p.serial_key) = (o.tenant_id, o.org_id, o.bank_id, o.serial_key)
      AND (p.status = 'processing'
           OR (p.status = 'pending' AND p.run_at <= now()
               AND (p.created_at, p.id) < (o.created_at, o.id))))
ORDER BY o.created_at
LIMIT $2
FOR UPDATE OF o SKIP LOCKED;

-- ClaimMark moves the selected jobs to processing under the worker with a fresh heartbeat.
-- name: ClaimMark :exec
UPDATE operations SET status = 'processing', worker_id = @worker_id, heartbeat_at = now(), updated_at = now()
WHERE id = ANY(@ids::uuid[]);

-- ClaimHeartbeat refreshes the worker's processing jobs and reports which have a cancel request.
-- name: ClaimHeartbeat :many
UPDATE operations SET heartbeat_at = now()
WHERE id = ANY(@ids::uuid[]) AND status = 'processing' AND worker_id = @worker_id
RETURNING id, (cancel_requested_at IS NOT NULL)::bool AS cancel_requested;

-- ClaimProgress records a processing job's checkpoint.
-- name: ClaimProgress :execrows
UPDATE operations SET progress = @progress, updated_at = now() WHERE id = @id AND status = 'processing';

-- ClaimAlive reports whether a job is processing without a cancel request, with the same scope rules as
-- ClaimSelect: a bank job in a live bank (delete_bank in its tombstoned one), an org-scoped job while its org row
-- exists, and under a tombstoned org only delete_bank and delete_org.
-- name: ClaimAlive :one
SELECT (o.status = 'processing' AND o.cancel_requested_at IS NULL AND g.id IS NOT NULL
        AND (g.deleting_at IS NULL OR o.kind IN ('delete_bank', 'delete_org'))
        AND (o.bank_id = '' OR (b.id IS NOT NULL AND (b.deleting_at IS NULL OR o.kind = 'delete_bank'))))::bool
       AS alive
FROM operations o
LEFT JOIN orgs g ON (g.tenant_id, g.id) = (o.tenant_id, o.org_id)
LEFT JOIN banks b ON (b.tenant_id, b.org_id, b.id) = (o.tenant_id, o.org_id, o.bank_id)
WHERE o.id = $1;

-- ClaimComplete finishes a processing job: COMPLETED, or CANCELLED when its cancel was requested.
-- name: ClaimComplete :one
UPDATE operations
SET status = CASE WHEN cancel_requested_at IS NULL THEN 'completed' ELSE 'cancelled' END,
    result = @result, usage = @usage, finished_at = now(), updated_at = now()
WHERE id = @id AND status = 'processing'
RETURNING status;

-- ClaimRetryLater returns a processing job to pending at run_at with attempts+1, or CANCELLED when its
-- cancel was requested.
-- name: ClaimRetryLater :one
UPDATE operations
SET status = CASE WHEN cancel_requested_at IS NULL THEN 'pending' ELSE 'cancelled' END,
    run_at = CASE WHEN cancel_requested_at IS NULL THEN @run_at::timestamptz ELSE run_at END,
    attempts = CASE WHEN cancel_requested_at IS NULL THEN attempts + 1 ELSE attempts END,
    finished_at = CASE WHEN cancel_requested_at IS NULL THEN NULL ELSE now() END,
    worker_id = CASE WHEN cancel_requested_at IS NULL THEN NULL ELSE worker_id END,
    heartbeat_at = CASE WHEN cancel_requested_at IS NULL THEN NULL ELSE heartbeat_at END,
    error = @error, updated_at = now()
WHERE id = @id AND status = 'processing'
RETURNING status;

-- ClaimPeek reads a job's bank, status and cancel request without locking it.
-- name: ClaimPeek :one
SELECT tenant_id, org_id, bank_id, kind, status, attempts, (cancel_requested_at IS NOT NULL)::bool AS cancel_requested
FROM operations WHERE id = $1;

-- ClaimFinishFailed ends a processing job FAILED, or CANCELLED when its cancel was requested. With
-- stale_secs set it only matches a job whose heartbeat is older than that and that has the given attempts.
-- name: ClaimFinishFailed :one
UPDATE operations
SET status = CASE WHEN cancel_requested_at IS NULL THEN 'failed' ELSE 'cancelled' END,
    error = COALESCE(sqlc.narg(error)::text, error), finished_at = now(), updated_at = now()
WHERE id = @id AND status = 'processing'
  AND (sqlc.narg(stale_secs)::float8 IS NULL
       OR (heartbeat_at < now() - make_interval(secs => sqlc.narg(stale_secs)::float8)
           AND attempts = @attempts::int))
RETURNING status;

-- ClaimStale lists processing jobs whose heartbeat is older than the ttl.
-- name: ClaimStale :many
SELECT id, kind, attempts, (cancel_requested_at IS NOT NULL)::bool AS cancel_requested
FROM operations
WHERE status = 'processing' AND heartbeat_at < now() - make_interval(secs => @stale_secs::float8)
ORDER BY heartbeat_at;

-- ClaimRequeueStale returns stale processing jobs without a cancel request to pending with attempts+1.
-- name: ClaimRequeueStale :execrows
UPDATE operations
SET status = 'pending', attempts = attempts + 1, worker_id = NULL, heartbeat_at = NULL, updated_at = now()
WHERE id = ANY(@ids::uuid[]) AND status = 'processing' AND cancel_requested_at IS NULL
  AND heartbeat_at < now() - make_interval(secs => @stale_secs::float8);

-- ClaimCancelStale moves stale cancel-requested processing jobs to CANCELLED.
-- name: ClaimCancelStale :execrows
UPDATE operations
SET status = 'cancelled', finished_at = now(), updated_at = now()
WHERE id = ANY(@ids::uuid[]) AND status = 'processing' AND cancel_requested_at IS NOT NULL
  AND heartbeat_at < now() - make_interval(secs => @stale_secs::float8);

-- ClaimAppendFailedEvent appends one OPERATION_FAILED lifecycle event.
-- name: ClaimAppendFailedEvent :exec
INSERT INTO lifecycle_events (tenant_id, org_id, bank_id, seq, type, operation_id, subject_id)
VALUES (@tenant_id, @org_id, @bank_id, @seq, 'operation_failed', @operation_id, '');
