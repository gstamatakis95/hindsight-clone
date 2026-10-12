-- Reads of batch ingestion (RetainBatch): the in-flight join and the backlog gate.

-- QueueActiveBySerialKey returns a bank's active jobs of one kind and serial key (pending or processing, no
-- cancel requested), newest first.
-- name: QueueActiveBySerialKey :many
SELECT * FROM operations
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND kind = @kind
  AND serial_key = @serial_key::text AND status IN ('pending', 'processing') AND cancel_requested_at IS NULL
ORDER BY created_at DESC, id DESC;

-- QueueCountPendingInBank counts a bank's pending jobs of one kind, up to lim.
-- name: QueueCountPendingInBank :one
SELECT count(*)::bigint FROM (
    SELECT 1 FROM operations
    WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND kind = @kind AND status = 'pending'
    LIMIT @lim
) AS p;

-- QueueCountPendingInOrg counts an org's pending jobs of one kind, across its banks, up to lim.
-- name: QueueCountPendingInOrg :one
SELECT count(*)::bigint FROM (
    SELECT 1 FROM operations
    WHERE tenant_id = @tenant_id AND org_id = @org_id AND kind = @kind AND status = 'pending'
    LIMIT @lim
) AS p;
