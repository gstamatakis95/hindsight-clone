-- GetUnits returns the live units of a bank with the given ids, with their entity names and cited sources.
-- name: GetUnits :many
SELECT sqlc.embed(u),
    COALESCE((SELECT array_agg(e.canonical_name ORDER BY e.canonical_name)
              FROM unit_entities ue JOIN entities e ON e.id = ue.entity_id
              WHERE ue.unit_id = u.id), '{}')::text[] AS entity_names,
    COALESCE((SELECT array_agg(ev.unit_id ORDER BY ev.unit_id)
              FROM observation_evidence ev WHERE ev.observation_id = u.id), '{}')::uuid[] AS source_ids
FROM memory_units u
WHERE u.tenant_id = $1 AND u.bank_id = $2 AND u.id = ANY(@ids::uuid[]);
