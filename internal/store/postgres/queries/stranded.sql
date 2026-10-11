-- StrandedFacts returns live facts left with a NULL embedding by a rederive_memory operation that ended
-- FAILED or CANCELLED (WP-14b, decision 1452), on banks that are not tombstoned, when no rederive of the unit
-- is pending or processing and none ended within retry_after_seconds. It walks the operations table (pruned
-- after the retention period), not memory_units, so the maintenance tick never scans every unit.
-- name: StrandedFacts :many
SELECT DISTINCT u.tenant_id, u.bank_id, u.id, u.document_id
FROM operations f
JOIN memory_units u
  ON u.tenant_id = f.tenant_id AND u.bank_id = f.bank_id
 AND u.id = (CASE WHEN f.payload->>'unit_id' ~ '^[0-9a-fA-F-]{36}$' THEN (f.payload->>'unit_id')::uuid END)
JOIN banks b ON b.tenant_id = u.tenant_id AND b.id = u.bank_id
WHERE f.kind = 'rederive_memory' AND f.status IN ('failed', 'cancelled')
  AND u.embedding IS NULL AND u.fact_type <> 'observation' AND b.deleting_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM operations o
    WHERE o.tenant_id = u.tenant_id AND o.bank_id = u.bank_id AND o.kind = 'rederive_memory'
      AND o.payload->>'unit_id' = u.id::text
      AND (o.status IN ('pending', 'processing')
           OR COALESCE(o.finished_at, o.updated_at) > now() - make_interval(secs => @retry_after_seconds::float8))
  )
ORDER BY u.tenant_id, u.bank_id, u.id
LIMIT @lim;
