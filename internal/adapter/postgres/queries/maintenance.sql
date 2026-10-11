-- UnconsolidatedBanks returns, in byte order, the banks that are not tombstoned and hold an unconsolidated fact
-- with an embedding created before @before (decision 2000): the §0 predicate as in ScopeUnconsolidated plus the
-- grace cut-off, over the partial index (tenant_id, bank_id, created_at).
-- name: UnconsolidatedBanks :many
SELECT b.tenant_id, b.id FROM banks b
WHERE b.deleting_at IS NULL AND EXISTS (
    SELECT 1 FROM memory_units u
    WHERE u.tenant_id = b.tenant_id AND u.bank_id = b.id
      AND u.consolidated_at IS NULL AND u.consolidation_failed_at IS NULL AND u.fact_type <> 'observation'
      AND u.embedding IS NOT NULL AND u.created_at < @before::timestamptz
)
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
