-- UnconsolidatedBanks returns, in byte order, the banks that are not tombstoned and hold an unconsolidated fact
-- with an embedding created before @before, each with its oldest such fact's created_at (the caller compares it
-- with the bank's own reconcile grace): the §0 predicate as in ScopeUnconsolidated, over the partial index
-- (tenant_id, bank_id, created_at).
-- name: UnconsolidatedBanks :many
SELECT b.tenant_id, b.id, o.created_at AS oldest FROM banks b
CROSS JOIN LATERAL (
    SELECT u.created_at FROM memory_units u
    WHERE u.tenant_id = b.tenant_id AND u.bank_id = b.id
      AND u.consolidated_at IS NULL AND u.consolidation_failed_at IS NULL AND u.fact_type <> 'observation'
      AND u.embedding IS NOT NULL AND u.created_at < @before::timestamptz
    ORDER BY u.created_at
    LIMIT 1
) o
WHERE b.deleting_at IS NULL
ORDER BY b.tenant_id COLLATE "C", b.id COLLATE "C";

-- StalledBankDeletes returns, in byte order, the tombstoned banks with no pending or processing delete_bank
-- operation (WP-20, decision 2001): their delete was failed by ReclaimStale or a shutdown release at the
-- attempt cap, or failed for good.
-- name: StalledBankDeletes :many
SELECT b.tenant_id, b.id FROM banks b
WHERE b.deleting_at IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM operations o
    WHERE o.tenant_id = b.tenant_id AND o.bank_id = b.id AND o.kind = 'delete_bank'
      AND o.status IN ('pending', 'processing')
)
ORDER BY b.tenant_id COLLATE "C", b.id COLLATE "C";

-- BanksWithGraphWork returns, in byte order, the banks that are not tombstoned and have graph_dirty or
-- entity_maintenance rows queued (WP-20, decision 2002).
-- name: BanksWithGraphWork :many
SELECT b.tenant_id, b.id FROM banks b
WHERE b.deleting_at IS NULL AND (
    EXISTS (SELECT 1 FROM graph_dirty g WHERE g.tenant_id = b.tenant_id AND g.bank_id = b.id)
    OR EXISTS (SELECT 1 FROM entity_maintenance e WHERE e.tenant_id = b.tenant_id AND e.bank_id = b.id)
)
ORDER BY b.tenant_id COLLATE "C", b.id COLLATE "C";

-- QueueStats returns the job queue's depth by kind and status (pending and processing only), with the age in
-- seconds of the oldest due pending job of each kind (run_at <= now(); 0 when none is due; parked retries and
-- debounced jobs are not waiting yet). retain_batch parents are left out: they are never claimed and stay pending
-- until their children settle. Both statuses are covered by the partial index operations_active_serial_key_idx.
-- name: QueueStats :many
SELECT kind, status, count(*)::bigint AS jobs,
    COALESCE(EXTRACT(EPOCH FROM now() - min(run_at) FILTER (WHERE status = 'pending' AND run_at <= now())), 0)::float8
        AS oldest_due_seconds
FROM operations
WHERE status IN ('pending', 'processing') AND kind <> 'retain_batch'
GROUP BY kind, status
ORDER BY kind, status;
