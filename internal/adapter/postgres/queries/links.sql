-- LinkInsert stores one edge; an existing (from, to, kind) edge keeps its row with the new weight.
-- name: LinkInsert :exec
INSERT INTO memory_links (tenant_id, bank_id, from_unit_id, to_unit_id, kind, weight, created_at)
VALUES (@tenant_id, @bank_id, @from_unit_id, @to_unit_id, @kind, @weight, COALESCE(sqlc.narg(created_at)::timestamptz, now()))
ON CONFLICT (from_unit_id, to_unit_id, kind) DO UPDATE SET weight = EXCLUDED.weight;
