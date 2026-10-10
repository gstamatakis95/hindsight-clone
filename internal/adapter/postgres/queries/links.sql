-- LinkInsert stores one edge; an existing (from, to, kind) edge keeps its row with the new weight.
-- name: LinkInsert :exec
INSERT INTO memory_links (tenant_id, bank_id, from_unit_id, to_unit_id, kind, weight, created_at)
VALUES (@tenant_id, @bank_id, @from_unit_id, @to_unit_id, @kind, @weight, COALESCE(sqlc.narg(created_at)::timestamptz, now()))
ON CONFLICT (from_unit_id, to_unit_id, kind) DO UPDATE SET weight = EXCLUDED.weight;

-- LinkDeleteForUnit removes the links of the given kinds (every kind when kinds is empty) touching a unit in either direction.
-- name: LinkDeleteForUnit :execrows
DELETE FROM memory_links
WHERE (from_unit_id = @unit_id OR to_unit_id = @unit_id)
  AND (cardinality(@kinds::text[]) = 0 OR kind = ANY(@kinds::text[]));

-- LinkCount counts the links of one kind touching a unit in either direction.
-- name: LinkCount :one
SELECT count(*)::int FROM memory_links
WHERE (from_unit_id = @unit_id OR to_unit_id = @unit_id) AND kind = @kind;

-- LinkDirty returns up to lim live units queued in a bank's graph_dirty, oldest first.
-- name: LinkDirty :many
SELECT g.unit_id FROM graph_dirty g
JOIN memory_units u ON u.id = g.unit_id AND u.tenant_id = g.tenant_id AND u.bank_id = g.bank_id
WHERE g.tenant_id = @tenant_id AND g.bank_id = @bank_id
ORDER BY g.created_at, g.unit_id
LIMIT @lim;

-- LinkClearDirty removes units from a bank's graph_dirty.
-- name: LinkClearDirty :exec
DELETE FROM graph_dirty WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND unit_id = ANY(@ids::uuid[]);
