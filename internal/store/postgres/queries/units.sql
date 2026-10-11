-- UnitInsert stores one unit exactly as given; a NULL created_at takes the database's now().
-- name: UnitInsert :exec
INSERT INTO memory_units (
    id, tenant_id, bank_id, document_id, chunk_id, fact_type, text, context, metadata, tags, embedding,
    occurred_start, occurred_end, mentioned_at, timeless, proof_count, scope_key, version, seq,
    consolidated_at, consolidation_failed_at, edited_at, created_at
) VALUES (
    @id, @tenant_id, @bank_id, @document_id, @chunk_id, @fact_type, @text, @context, @metadata, @tags,
    @embedding, @occurred_start, @occurred_end, @mentioned_at, @timeless, @proof_count, @scope_key, @version,
    @seq, @consolidated_at, @consolidation_failed_at, @edited_at, COALESCE(sqlc.narg(created_at)::timestamptz, now())
);

-- UnitUpsertObservation inserts an observation, or replaces the text, proof count, embedding, version and seq of the same bank's observation with that id; an id held by any other row affects nothing.
-- name: UnitUpsertObservation :execrows
INSERT INTO memory_units AS m (
    id, tenant_id, bank_id, document_id, chunk_id, fact_type, text, context, metadata, tags, embedding,
    occurred_start, occurred_end, mentioned_at, timeless, proof_count, scope_key, version, seq,
    consolidated_at, consolidation_failed_at, edited_at, created_at
) VALUES (
    @id, @tenant_id, @bank_id, @document_id, @chunk_id, @fact_type, @text, @context, @metadata, @tags,
    @embedding, @occurred_start, @occurred_end, @mentioned_at, @timeless, @proof_count, @scope_key, @version,
    @seq, @consolidated_at, @consolidation_failed_at, @edited_at, COALESCE(sqlc.narg(created_at)::timestamptz, now())
)
ON CONFLICT (id) DO UPDATE SET
    text = EXCLUDED.text, proof_count = EXCLUDED.proof_count, embedding = EXCLUDED.embedding,
    version = EXCLUDED.version, seq = EXCLUDED.seq, updated_at = now()
WHERE m.fact_type = 'observation' AND m.tenant_id = EXCLUDED.tenant_id AND m.bank_id = EXCLUDED.bank_id;

-- UnitScopeUpsert records an observation scope of a bank; an existing scope only gets a new updated_at.
-- name: UnitScopeUpsert :exec
INSERT INTO observation_scopes (tenant_id, bank_id, scope_key, tags)
VALUES (@tenant_id, @bank_id, @scope_key, @tags)
ON CONFLICT (tenant_id, bank_id, scope_key) DO UPDATE SET updated_at = now();

-- UnitRecordObservationVersion appends one history row of an observation; a NULL at takes now().
-- name: UnitRecordObservationVersion :exec
INSERT INTO observation_versions (tenant_id, bank_id, observation_id, version, text, source_ids, at)
VALUES (
    @tenant_id, @bank_id, @observation_id, @version, @text, @source_ids::uuid[],
    COALESCE(sqlc.narg(at)::timestamptz, now())
);

-- UnitUpdate replaces a live fact's curated fields and embedding and clears its consolidation stamps.
-- name: UnitUpdate :execrows
UPDATE memory_units SET
    text = @text, context = @context, occurred_start = @occurred_start, occurred_end = @occurred_end,
    timeless = @timeless, fact_type = @fact_type, edited_at = @edited_at, seq = @seq, embedding = @embedding,
    consolidated_at = NULL, consolidation_failed_at = NULL, updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND bank_id = @bank_id AND fact_type <> 'observation';

-- UnitSetDocumentTags replaces the document-derived tags of a document's live units with
-- (tags - old_tags) + new_tags (deduplicated, byte-sorted like domain.NormalizeTags), sets seq, clears their
-- consolidation stamps and returns their ids.
-- name: UnitSetDocumentTags :many
UPDATE memory_units SET
    tags = ARRAY(
        SELECT t FROM unnest(tags || @new_tags::text[]) AS t
        WHERE t <> ALL(@old_tags::text[]) OR t = ANY(@new_tags::text[])
        GROUP BY t ORDER BY t COLLATE "C"
    ),
    seq = @seq, consolidated_at = NULL, consolidation_failed_at = NULL, updated_at = now()
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id::text
RETURNING id;

-- UnitSetArchivedDocumentTags replaces the document-derived tags of a document's archived units, like
-- UnitSetDocumentTags.
-- name: UnitSetArchivedDocumentTags :exec
UPDATE memory_units_archive SET
    tags = ARRAY(
        SELECT t FROM unnest(tags || @new_tags::text[]) AS t
        WHERE t <> ALL(@old_tags::text[]) OR t = ANY(@new_tags::text[])
        GROUP BY t ORDER BY t COLLATE "C"
    )
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id;

-- UnitAppendAudit writes one memory_audit row; a NULL at takes now().
-- name: UnitAppendAudit :exec
INSERT INTO memory_audit (tenant_id, bank_id, unit_id, action, reason, before, after, operation_id, at)
VALUES (
    @tenant_id, @bank_id, @unit_id, @action, @reason, @before, @after, @operation_id,
    COALESCE(sqlc.narg(at)::timestamptz, now())
);

-- UnitUnconsolidated returns a bank's oldest facts with neither consolidation stamp and with an embedding (a unit awaiting its rederive is no consolidation candidate), with their entity names; with filter_scopes, only those whose tags contain one of the tag sets in scopes (a JSON array of arrays). The predicate is written to match the partial index memory_units_unconsolidated_idx.
-- name: UnitUnconsolidated :many
SELECT sqlc.embed(u),
    COALESCE((SELECT array_agg(e.canonical_name ORDER BY e.canonical_name)
              FROM unit_entities ue JOIN entities e ON e.id = ue.entity_id
              WHERE ue.unit_id = u.id), '{}')::text[] AS entity_names
FROM memory_units u
WHERE u.tenant_id = @tenant_id AND u.bank_id = @bank_id
  AND u.consolidated_at IS NULL AND u.consolidation_failed_at IS NULL AND u.fact_type <> 'observation'
  AND u.embedding IS NOT NULL
  AND (NOT @filter_scopes::bool OR EXISTS (
      SELECT 1 FROM jsonb_array_elements(@scopes::jsonb) AS s(tags)
      WHERE u.tags @> ARRAY(SELECT jsonb_array_elements_text(s.tags))))
ORDER BY u.created_at, u.id
LIMIT @lim;

-- UnitIDsByDocument returns the ids of a document's live units, sorted.
-- name: UnitIDsByDocument :many
SELECT id FROM memory_units
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id::text
ORDER BY id;

-- UnitMarkConsolidated stamps consolidated_at on the units.
-- name: UnitMarkConsolidated :exec
UPDATE memory_units SET consolidated_at = @at::timestamptz WHERE id = ANY(@ids::uuid[]);

-- UnitMarkConsolidationFailed stamps consolidation_failed_at on the units.
-- name: UnitMarkConsolidationFailed :exec
UPDATE memory_units SET consolidation_failed_at = @at::timestamptz WHERE id = ANY(@ids::uuid[]);

-- UnitLockDocument locks a document's live units in id order and returns their ids.
-- name: UnitLockDocument :many
SELECT id FROM memory_units
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id::text
ORDER BY id FOR UPDATE;

-- UnitLockIDs locks the bank's live units with the given ids in id order and returns the ids found.
-- name: UnitLockIDs :many
SELECT id FROM memory_units
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[])
ORDER BY id FOR UPDATE;

-- UnitQueueNeighbours records in graph_dirty the link neighbours of the units that are not among them.
-- name: UnitQueueNeighbours :exec
INSERT INTO graph_dirty (tenant_id, bank_id, unit_id)
SELECT DISTINCT n.tenant_id, n.bank_id, n.id FROM (
    SELECT l.tenant_id, l.bank_id, l.to_unit_id AS id FROM memory_links l WHERE l.from_unit_id = ANY(@ids::uuid[])
    UNION
    SELECT l.tenant_id, l.bank_id, l.from_unit_id AS id FROM memory_links l WHERE l.to_unit_id = ANY(@ids::uuid[])
) AS n
WHERE n.id <> ALL(@ids::uuid[])
ON CONFLICT DO NOTHING;

-- UnitQueueEntities records in entity_maintenance the entities of the units and takes their mentions off mention_count.
-- name: UnitQueueEntities :exec
WITH ue AS (
    SELECT x.tenant_id, x.bank_id, x.entity_id, count(*)::int AS n
    FROM unit_entities x WHERE x.unit_id = ANY(@ids::uuid[])
    GROUP BY x.tenant_id, x.bank_id, x.entity_id
), queued AS (
    INSERT INTO entity_maintenance (tenant_id, bank_id, entity_id)
    SELECT ue.tenant_id, ue.bank_id, ue.entity_id FROM ue
    ON CONFLICT DO NOTHING
)
UPDATE entities e SET mention_count = GREATEST(e.mention_count - ue.n, 0), updated_at = now()
FROM ue WHERE e.id = ue.entity_id;

-- UnitDeleteIDs deletes the bank's live units with the given ids (links, entity rows and evidence go by cascade) and drops them from graph_dirty.
-- name: UnitDeleteIDs :exec
WITH gone AS (
    DELETE FROM memory_units
    WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[])
    RETURNING id
)
DELETE FROM graph_dirty g
WHERE g.tenant_id = @tenant_id AND g.bank_id = @bank_id AND g.unit_id IN (SELECT gone.id FROM gone);

-- UnitArchive copies a live unit into memory_units_archive, stamped with seq, with JSON snapshots of its entities ({id, name}) and causal links ({from, to, kind, weight, created_at}).
-- name: UnitArchive :execrows
INSERT INTO memory_units_archive (
    id, tenant_id, bank_id, document_id, chunk_id, fact_type, text, context, metadata, tags,
    occurred_start, occurred_end, mentioned_at, timeless, seq, edited_at, entities_snapshot, links_snapshot,
    reason, created_at
)
SELECT u.id, u.tenant_id, u.bank_id, u.document_id, u.chunk_id, u.fact_type, u.text, u.context, u.metadata,
    u.tags, u.occurred_start, u.occurred_end, u.mentioned_at, u.timeless, @seq::bigint, u.edited_at,
    COALESCE((SELECT jsonb_agg(jsonb_build_object('id', e.id, 'name', e.canonical_name) ORDER BY e.id)
              FROM unit_entities ue JOIN entities e ON e.id = ue.entity_id
              WHERE ue.unit_id = u.id), '[]'::jsonb),
    COALESCE((SELECT jsonb_agg(jsonb_build_object(
                  'from', l.from_unit_id, 'to', l.to_unit_id, 'kind', l.kind, 'weight', l.weight,
                  'created_at', l.created_at) ORDER BY l.from_unit_id, l.to_unit_id)
              FROM memory_links l
              WHERE l.kind = 'causal' AND (l.from_unit_id = u.id OR l.to_unit_id = u.id)), '[]'::jsonb),
    @reason::text, u.created_at
FROM memory_units u
WHERE u.id = @id AND u.tenant_id = @tenant_id AND u.bank_id = @bank_id;

-- UnitTakeArchived deletes a bank's archived unit and returns its row.
-- name: UnitTakeArchived :one
DELETE FROM memory_units_archive
WHERE id = @id AND tenant_id = @tenant_id AND bank_id = @bank_id
RETURNING *;

-- UnitRestore inserts an archived unit back as a live, unconsolidated unit without embedding; a chunk that no longer exists is dropped.
-- name: UnitRestore :exec
INSERT INTO memory_units (
    id, tenant_id, bank_id, document_id, chunk_id, fact_type, text, context, metadata, tags,
    occurred_start, occurred_end, mentioned_at, timeless, scope_key, seq, edited_at, created_at
) VALUES (
    @id, @tenant_id, @bank_id, @document_id, (SELECT c.id FROM chunks c WHERE c.id = sqlc.narg(chunk_id)::uuid),
    @fact_type, @text, @context, @metadata, @tags, @occurred_start, @occurred_end, @mentioned_at, @timeless,
    @scope_key, @seq, @edited_at, @created_at
);

-- UnitRestoreEntities reattaches a restored unit to the snapshot's entities that still exist in its bank (locked in id order, so concurrent writers can't deadlock on them) and counts their mentions back.
-- name: UnitRestoreEntities :exec
WITH locked AS (
    SELECT e.tenant_id, e.bank_id, e.id FROM entities e
    WHERE e.tenant_id = @tenant_id AND e.bank_id = @bank_id
      AND e.id IN (SELECT (x.v->>'id')::uuid FROM jsonb_array_elements(@snapshot::jsonb) AS x(v))
    ORDER BY e.id
    FOR UPDATE
), ins AS (
    INSERT INTO unit_entities (tenant_id, bank_id, unit_id, entity_id)
    SELECT l.tenant_id, l.bank_id, @unit_id::uuid, l.id FROM locked l
    ON CONFLICT DO NOTHING
    RETURNING entity_id
)
UPDATE entities e SET mention_count = e.mention_count + 1, updated_at = now()
FROM ins WHERE e.id = ins.entity_id;

-- UnitRestoreLinks reattaches the snapshot's causal links whose partner is live in the bank.
-- name: UnitRestoreLinks :exec
INSERT INTO memory_links (tenant_id, bank_id, from_unit_id, to_unit_id, kind, weight, created_at)
SELECT p.tenant_id, p.bank_id, s.from_id, s.to_id, 'causal', s.weight, s.created_at
FROM (
    SELECT (x.v->>'from')::uuid AS from_id, (x.v->>'to')::uuid AS to_id,
        COALESCE((x.v->>'weight')::float8, 1) AS weight,
        COALESCE((x.v->>'created_at')::timestamptz, now()) AS created_at
    FROM jsonb_array_elements(@snapshot::jsonb) AS x(v)
) AS s
JOIN memory_units p ON p.id = CASE WHEN s.from_id = @unit_id::uuid THEN s.to_id ELSE s.from_id END
WHERE p.tenant_id = @tenant_id AND p.bank_id = @bank_id AND s.from_id <> s.to_id
ON CONFLICT (from_unit_id, to_unit_id, kind) DO NOTHING;

-- UnitHandOverLinks appends the snapshot's causal links whose partner is itself archived in the bank to that partner's links snapshot, so restoring the partner later restores them.
-- name: UnitHandOverLinks :exec
UPDATE memory_units_archive a SET links_snapshot = a.links_snapshot || s.links
FROM (
    SELECT CASE WHEN (x.v->>'from')::uuid = @unit_id::uuid THEN (x.v->>'to')::uuid
                ELSE (x.v->>'from')::uuid END AS partner,
        jsonb_agg(x.v) AS links
    FROM jsonb_array_elements(@snapshot::jsonb) AS x(v)
    GROUP BY 1
) AS s
WHERE a.id = s.partner AND a.tenant_id = @tenant_id AND a.bank_id = @bank_id;
