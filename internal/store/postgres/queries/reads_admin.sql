-- WP-15 admin reads (decision 1500): tag counts, bank stats, observation history, mental-model listings, cron and
-- failed-refresh sweeps, and document listings. Text keysets and orderings use COLLATE "C" (byte order). LIKE
-- patterns arrive already escaped (%, _ and \ literal) from the Go side.

-- ReadsTagCounts counts, per tag, the items of one source ('memories': live units, 'mental_models',
-- 'documents') of a bank carrying it, keeping tags that match prefix_like and sort after after ('' = from the
-- start), in byte order; a NULL lim returns all.
-- name: ReadsTagCounts :many
WITH items AS (
    SELECT u.tags FROM memory_units u
    WHERE @source::text = 'memories' AND u.tenant_id = @tenant_id AND u.bank_id = @bank_id
    UNION ALL
    SELECT m.tags FROM mental_models m
    WHERE @source::text = 'mental_models' AND m.tenant_id = @tenant_id AND m.bank_id = @bank_id
    UNION ALL
    SELECT d.tags FROM documents d
    WHERE @source::text = 'documents' AND d.tenant_id = @tenant_id AND d.bank_id = @bank_id
)
SELECT t.tag::text AS tag, count(*)::bigint AS n
FROM items i CROSS JOIN LATERAL unnest(i.tags) AS t(tag)
WHERE t.tag LIKE @prefix_like::text
  AND (@after::text = '' OR t.tag COLLATE "C" > @after::text)
GROUP BY t.tag
ORDER BY t.tag COLLATE "C"
LIMIT sqlc.narg(lim)::int;

-- ReadsBankStats returns a bank row with its counts in one statement (one snapshot). The per-type maps are JSON
-- objects (type -> count). No row means the bank is unknown.
-- name: ReadsBankStats :one
SELECT b.deleting_at, b.last_memory_write_at, b.change_seq,
    COALESCE((SELECT jsonb_object_agg(c.fact_type, c.n) FROM (
        SELECT u.fact_type, count(*) AS n FROM memory_units u
        WHERE u.tenant_id = b.tenant_id AND u.bank_id = b.id GROUP BY u.fact_type) c),
        '{}')::jsonb AS units_by_fact_type,
    (SELECT count(*) FROM memory_units_archive a
     WHERE a.tenant_id = b.tenant_id AND a.bank_id = b.id)::bigint AS invalidated_units,
    (SELECT count(*) FROM documents d WHERE d.tenant_id = b.tenant_id AND d.bank_id = b.id)::bigint AS documents,
    (SELECT count(*) FROM entities e WHERE e.tenant_id = b.tenant_id AND e.bank_id = b.id)::bigint AS entities,
    (SELECT count(*) FROM mental_models m
     WHERE m.tenant_id = b.tenant_id AND m.bank_id = b.id)::bigint AS mental_models,
    COALESCE((SELECT jsonb_object_agg(c.kind, c.n) FROM (
        SELECT o.kind, count(*) AS n FROM operations o
        WHERE o.tenant_id = b.tenant_id AND o.bank_id = b.id AND o.status IN ('pending', 'processing')
        GROUP BY o.kind) c),
        '{}')::jsonb AS pending_operations_by_kind,
    (SELECT count(*) FROM memory_units f
     WHERE f.tenant_id = b.tenant_id AND f.bank_id = b.id AND f.fact_type <> 'observation'
       AND f.consolidated_at IS NULL AND f.consolidation_failed_at IS NULL)::bigint AS unconsolidated_units
FROM banks b
WHERE b.tenant_id = @tenant_id AND b.id = @bank_id;

-- ReadsObservationHistory returns an observation's version rows, newest first.
-- name: ReadsObservationHistory :many
SELECT v.version, v.text, v.source_ids, v.at
FROM observation_versions v
WHERE v.tenant_id = @tenant_id AND v.bank_id = @bank_id AND v.observation_id = @observation_id
ORDER BY v.version DESC, v.at DESC, v.id DESC;

-- ReadsListMentalModels returns a bank's models ordered by id (byte order) after after_id ('' = from the start):
-- folder '' or folder_path = folder or LIKE folder_like (folder escaped, then '/%'); tags with
-- domain.MatchTags semantics under tags_match (filter tags normalized, never NULL); refresh_after keeps only
-- models whose trigger sets refresh_after_consolidation. A NULL lim returns all.
-- name: ReadsListMentalModels :many
SELECT * FROM mental_models
WHERE tenant_id = @tenant_id AND bank_id = @bank_id
  AND (@folder::text = '' OR folder_path = @folder::text OR folder_path LIKE @folder_like::text)
  AND (NOT @refresh_after::bool OR COALESCE((trigger ->> 'refresh_after_consolidation')::boolean, false))
  AND (@after_id::text = '' OR id COLLATE "C" > @after_id::text)
  AND (CASE @tags_match::text
      WHEN 'exact' THEN tags @> @filter_tags::text[] AND tags <@ @filter_tags::text[]
      WHEN 'any_strict' THEN cardinality(@filter_tags::text[]) = 0 OR tags && @filter_tags::text[]
      WHEN 'all_strict' THEN tags @> @filter_tags::text[]
      WHEN 'all' THEN cardinality(@filter_tags::text[]) = 0 OR cardinality(tags) = 0 OR tags @> @filter_tags::text[]
      ELSE cardinality(@filter_tags::text[]) = 0 OR cardinality(tags) = 0 OR tags && @filter_tags::text[]
  END)
ORDER BY id COLLATE "C"
LIMIT sqlc.narg(lim)::int;

-- ReadsCronModels returns every model with a non-empty refresh_cron on a bank that is not tombstoned, with the
-- created_at of its newest refresh_mental_model operation (any status; enqueued is false when it has none),
-- ordered by tenant, bank and id in byte order.
-- name: ReadsCronModels :many
SELECT sqlc.embed(m), (lo.created_at IS NOT NULL)::bool AS enqueued,
    COALESCE(lo.created_at, 'epoch'::timestamptz)::timestamptz AS last_enqueued_at
FROM mental_models m
JOIN banks b ON b.tenant_id = m.tenant_id AND b.id = m.bank_id
LEFT JOIN LATERAL (
    SELECT o.created_at FROM operations o
    WHERE o.tenant_id = m.tenant_id AND o.bank_id = m.bank_id AND o.kind = 'refresh_mental_model'
      AND o.serial_key = 'mm:' || m.id
    ORDER BY o.created_at DESC
    LIMIT 1
) lo ON TRUE
WHERE b.deleting_at IS NULL AND m.refresh_cron IS NOT NULL AND m.refresh_cron <> ''
ORDER BY m.tenant_id COLLATE "C", m.bank_id COLLATE "C", m.id COLLATE "C";

-- ReadsFailedRefreshes returns, per existing model on a bank that is not tombstoned, its latest completed or
-- failed refresh_mental_model operation (finished_at, created_at, id, all descending; cancelled ones ignored)
-- when that operation failed, ordered by tenant, bank and model id in byte order.
-- name: ReadsFailedRefreshes :many
SELECT m.tenant_id, m.bank_id, m.id AS model_id, l.id AS operation_id, l.finished_at,
    COALESCE(l.error, '')::text AS error
FROM (
    SELECT DISTINCT ON (o.tenant_id, o.bank_id, o.serial_key)
        o.tenant_id, o.bank_id, o.serial_key, o.id, o.status, o.finished_at, o.error
    FROM operations o
    WHERE o.kind = 'refresh_mental_model' AND o.status IN ('completed', 'failed') AND o.serial_key LIKE 'mm:%'
    ORDER BY o.tenant_id, o.bank_id, o.serial_key, o.finished_at DESC NULLS LAST, o.created_at DESC, o.id DESC
) l
JOIN mental_models m ON m.tenant_id = l.tenant_id AND m.bank_id = l.bank_id AND l.serial_key = 'mm:' || m.id
JOIN banks b ON b.tenant_id = m.tenant_id AND b.id = m.bank_id
WHERE l.status = 'failed' AND b.deleting_at IS NULL
ORDER BY m.tenant_id COLLATE "C", m.bank_id COLLATE "C", m.id COLLATE "C";

-- ReadsListDocuments returns a bank's documents newest first ((created_at, id) descending, id in byte order)
-- strictly before the cursor (NULL after_created_at = first page), with their live unit counts per fact type as
-- a JSON object. id_like '' = no id filter; tags use domain.MatchTags semantics under tags_match; time_field
-- 'updated_at' bounds updated_at, anything else created_at (start inclusive, end exclusive).
-- name: ReadsListDocuments :many
SELECT sqlc.embed(d),
    COALESCE((SELECT jsonb_object_agg(c.fact_type, c.n) FROM (
        SELECT u.fact_type, count(*) AS n FROM memory_units u
        WHERE u.tenant_id = d.tenant_id AND u.bank_id = d.bank_id AND u.document_id = d.id
        GROUP BY u.fact_type) c),
        '{}')::jsonb AS units_by_fact_type
FROM documents d
WHERE d.tenant_id = @tenant_id AND d.bank_id = @bank_id
  AND (@id_like::text = '' OR d.id ILIKE @id_like::text)
  AND (CASE @tags_match::text
      WHEN 'exact' THEN d.tags @> @filter_tags::text[] AND d.tags <@ @filter_tags::text[]
      WHEN 'any_strict' THEN cardinality(@filter_tags::text[]) = 0 OR d.tags && @filter_tags::text[]
      WHEN 'all_strict' THEN d.tags @> @filter_tags::text[]
      WHEN 'all' THEN cardinality(@filter_tags::text[]) = 0 OR cardinality(d.tags) = 0
          OR d.tags @> @filter_tags::text[]
      ELSE cardinality(@filter_tags::text[]) = 0 OR cardinality(d.tags) = 0 OR d.tags && @filter_tags::text[]
  END)
  AND (sqlc.narg(start_at)::timestamptz IS NULL
      OR (CASE WHEN @time_field::text = 'updated_at' THEN d.updated_at ELSE d.created_at END)
          >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL
      OR (CASE WHEN @time_field::text = 'updated_at' THEN d.updated_at ELSE d.created_at END)
          < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
      OR d.created_at < sqlc.narg(after_created_at)::timestamptz
      OR (d.created_at = sqlc.narg(after_created_at)::timestamptz AND d.id COLLATE "C" < @after_id::text))
ORDER BY d.created_at DESC, d.id COLLATE "C" DESC
LIMIT @lim::int;
