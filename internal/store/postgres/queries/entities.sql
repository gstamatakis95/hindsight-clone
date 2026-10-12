-- EntityCandidates returns up to lim entities of a bank whose lower-cased names are trigram-similar (pg_trgm %) to the lower-cased name, most similar first.
-- name: EntityCandidates :many
SELECT sqlc.embed(e), similarity(lower(e.canonical_name), lower(@name::text))::float8 AS sim
FROM entities e
WHERE e.tenant_id = @tenant_id AND e.org_id = @org_id AND e.bank_id = @bank_id AND lower(e.canonical_name) % lower(@name::text)
ORDER BY sim DESC, e.id
LIMIT @lim;

-- EntityCooccurrence counts how many of others have a co-occurrence row with the entity.
-- name: EntityCooccurrence :one
SELECT count(*)::int FROM entity_cooccurrences c
WHERE c.tenant_id = @tenant_id AND c.org_id = @org_id AND c.bank_id = @bank_id AND c.count > 0
  AND ((c.entity_a = @entity_id AND c.entity_b = ANY(@others::uuid[]))
       OR (c.entity_b = @entity_id AND c.entity_a = ANY(@others::uuid[])));

-- EntityUpdate replaces a bank's entity's name, kind and label value, widens its first and last seen, and returns its id.
-- name: EntityUpdate :one
UPDATE entities SET
    canonical_name = @canonical_name, kind = @kind, label_value = @label_value,
    first_seen = LEAST(first_seen, sqlc.narg(first_seen)::timestamptz),
    last_seen = GREATEST(last_seen, sqlc.narg(last_seen)::timestamptz), updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id
RETURNING id;

-- EntityInsert inserts an entity and returns its id; a label entity whose (kind, label value) exists resolves to that row.
-- name: EntityInsert :one
INSERT INTO entities AS x (id, tenant_id, org_id, bank_id, canonical_name, kind, label_value, first_seen, last_seen)
VALUES (@id, @tenant_id, @org_id, @bank_id, @canonical_name, @kind, @label_value, @first_seen, @last_seen)
ON CONFLICT (tenant_id, org_id, bank_id, kind, label_value) WHERE label_value IS NOT NULL
DO UPDATE SET first_seen = LEAST(x.first_seen, EXCLUDED.first_seen),
    last_seen = GREATEST(x.last_seen, EXCLUDED.last_seen), updated_at = now()
RETURNING id;

-- EntityUnitBank returns the tenant and bank of a live unit.
-- name: EntityUnitBank :one
SELECT tenant_id, org_id, bank_id FROM memory_units WHERE id = @id;

-- EntityLock locks the bank's entities with the given ids in id order (FOR NO KEY UPDATE) and returns the ids found.
-- name: EntityLock :many
SELECT id FROM entities
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[])
ORDER BY id FOR NO KEY UPDATE;

-- EntityAttach links a unit to entities, skipping existing rows, and returns the newly linked entity ids.
-- name: EntityAttach :many
INSERT INTO unit_entities (tenant_id, org_id, bank_id, unit_id, entity_id)
SELECT @tenant_id::text, @org_id::text, @bank_id::text, @unit_id::uuid, x.id FROM unnest(@ids::uuid[]) AS x(id)
ON CONFLICT DO NOTHING
RETURNING entity_id;

-- EntityBumpMentions adds one mention to each of the entities.
-- name: EntityBumpMentions :exec
UPDATE entities SET mention_count = mention_count + 1, updated_at = now() WHERE id = ANY(@ids::uuid[]);

-- EntityBumpCooccurrences counts one more co-occurrence for every pair of a newly attached entity with another entity of the unit.
-- name: EntityBumpCooccurrences :exec
INSERT INTO entity_cooccurrences (tenant_id, org_id, bank_id, entity_a, entity_b, count, last_at)
SELECT @tenant_id::text, @org_id::text, @bank_id::text, LEAST(n.id, ue.entity_id), GREATEST(n.id, ue.entity_id), 1, now()
FROM unnest(@new_ids::uuid[]) AS n(id)
JOIN unit_entities ue ON ue.unit_id = @unit_id::uuid AND ue.entity_id <> n.id
WHERE ue.entity_id <> ALL(@new_ids::uuid[]) OR n.id < ue.entity_id
ON CONFLICT (entity_a, entity_b) DO UPDATE SET count = entity_cooccurrences.count + 1, last_at = now();

-- EntityPendingMaintenance returns up to lim entity ids queued in a bank's entity_maintenance, oldest first.
-- name: EntityPendingMaintenance :many
SELECT entity_id FROM entity_maintenance
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id
ORDER BY created_at, entity_id
LIMIT @lim;

-- EntityDetach removes a unit's entity rows, queues the entities in entity_maintenance and takes the mentions off mention_count.
-- name: EntityDetach :exec
WITH d AS (
    DELETE FROM unit_entities WHERE unit_id = @unit_id RETURNING tenant_id, org_id, bank_id, entity_id
), queued AS (
    INSERT INTO entity_maintenance (tenant_id, org_id, bank_id, entity_id)
    SELECT d.tenant_id, d.org_id, d.bank_id, d.entity_id FROM d
    ON CONFLICT DO NOTHING
)
UPDATE entities e SET mention_count = GREATEST(e.mention_count - 1, 0), updated_at = now()
FROM d WHERE e.id = d.entity_id;

-- EntityLockForDelete locks the bank's entities with the given ids in id order (FOR UPDATE), so no unit can attach them until commit.
-- name: EntityLockForDelete :many
SELECT id FROM entities
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[])
ORDER BY id FOR UPDATE;

-- EntityDeleteOrphans deletes those of the bank's entities that no unit references (their co-occurrences go by cascade).
-- name: EntityDeleteOrphans :execrows
DELETE FROM entities e
WHERE e.tenant_id = @tenant_id AND e.org_id = @org_id AND e.bank_id = @bank_id AND e.id = ANY(@ids::uuid[])
  AND NOT EXISTS (SELECT 1 FROM unit_entities ue WHERE ue.entity_id = e.id);

-- EntityClearMaintenance removes entities from a bank's entity_maintenance.
-- name: EntityClearMaintenance :exec
DELETE FROM entity_maintenance
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND entity_id = ANY(@ids::uuid[]);
