-- WP-15 unit reads (decision 1500): GetUnit and ListUnits over live and archived units.

-- ReadsListUnits returns up to lim units of a bank, newest first ((created_at, id) descending) and strictly
-- before the cursor (NULL after_created_at = first page), from memory_units (include_live) and
-- memory_units_archive (include_archived). Optional filters: unit_id, types (empty = all), text_like ('' =
-- none; ILIKE pattern), document_id, entity_id (live units only: archived units never match), start_at
-- (inclusive) and end_at (exclusive) on time_field (a NULL field never matches; an archived unit's updated_at
-- is archived_at), and tags with domain.MatchTags semantics under tags_match (filter tags normalized, never
-- NULL). Each row carries its entity names (live: current names; archived: snapshot names, byte order), its
-- cited sources (live observations) and stale (a live observation with an unconsolidated live fact of the bank
-- whose tags contain the observation's tags).
-- name: ReadsListUnits :many
WITH picked AS (
    (SELECT u.id, u.tenant_id, u.bank_id, u.document_id, u.chunk_id, u.fact_type, u.text, u.context, u.metadata,
        u.tags, u.embedding, u.occurred_start, u.occurred_end, u.mentioned_at, u.timeless, u.proof_count,
        u.version, u.seq, u.consolidated_at, u.consolidation_failed_at, u.edited_at, u.created_at, u.updated_at,
        TRUE AS live, '[]'::jsonb AS entities_snapshot
    FROM memory_units u
    WHERE @include_live::bool AND u.tenant_id = @tenant_id AND u.bank_id = @bank_id
      AND (sqlc.narg(unit_id)::uuid IS NULL OR u.id = sqlc.narg(unit_id)::uuid)
      AND (cardinality(@types::text[]) = 0 OR u.fact_type = ANY(@types::text[]))
      AND (@text_like::text = '' OR u.text ILIKE @text_like::text)
      AND (sqlc.narg(document_id)::text IS NULL OR u.document_id = sqlc.narg(document_id)::text)
      AND (sqlc.narg(entity_id)::uuid IS NULL OR EXISTS (
          SELECT 1 FROM unit_entities ue WHERE ue.unit_id = u.id AND ue.entity_id = sqlc.narg(entity_id)::uuid))
      AND (sqlc.narg(start_at)::timestamptz IS NULL OR (CASE @time_field::text
          WHEN 'updated_at' THEN u.updated_at WHEN 'mentioned_at' THEN u.mentioned_at
          WHEN 'occurred_start' THEN u.occurred_start WHEN 'occurred_end' THEN u.occurred_end
          ELSE u.created_at END) >= sqlc.narg(start_at)::timestamptz)
      AND (sqlc.narg(end_at)::timestamptz IS NULL OR (CASE @time_field::text
          WHEN 'updated_at' THEN u.updated_at WHEN 'mentioned_at' THEN u.mentioned_at
          WHEN 'occurred_start' THEN u.occurred_start WHEN 'occurred_end' THEN u.occurred_end
          ELSE u.created_at END) < sqlc.narg(end_at)::timestamptz)
      AND (CASE @tags_match::text
          WHEN 'exact' THEN u.tags @> @filter_tags::text[] AND u.tags <@ @filter_tags::text[]
          WHEN 'any_strict' THEN cardinality(@filter_tags::text[]) = 0 OR u.tags && @filter_tags::text[]
          WHEN 'all_strict' THEN u.tags @> @filter_tags::text[]
          WHEN 'all' THEN cardinality(@filter_tags::text[]) = 0 OR cardinality(u.tags) = 0
              OR u.tags @> @filter_tags::text[]
          ELSE cardinality(@filter_tags::text[]) = 0 OR cardinality(u.tags) = 0 OR u.tags && @filter_tags::text[]
      END)
      AND (sqlc.narg(after_created_at)::timestamptz IS NULL
          OR (u.created_at, u.id) < (sqlc.narg(after_created_at)::timestamptz, @after_id::uuid))
    ORDER BY u.created_at DESC, u.id DESC
    LIMIT @lim::int)
    UNION ALL
    (SELECT a.id, a.tenant_id, a.bank_id, a.document_id, a.chunk_id, a.fact_type, a.text, a.context, a.metadata,
        a.tags, NULL::vector, a.occurred_start, a.occurred_end, a.mentioned_at, a.timeless, 0::int,
        0::bigint, a.seq, NULL::timestamptz, NULL::timestamptz, a.edited_at, a.created_at, a.archived_at,
        FALSE, a.entities_snapshot
    FROM memory_units_archive a
    WHERE @include_archived::bool AND a.tenant_id = @tenant_id AND a.bank_id = @bank_id
      AND sqlc.narg(entity_id)::uuid IS NULL
      AND (sqlc.narg(unit_id)::uuid IS NULL OR a.id = sqlc.narg(unit_id)::uuid)
      AND (cardinality(@types::text[]) = 0 OR a.fact_type = ANY(@types::text[]))
      AND (@text_like::text = '' OR a.text ILIKE @text_like::text)
      AND (sqlc.narg(document_id)::text IS NULL OR a.document_id = sqlc.narg(document_id)::text)
      AND (sqlc.narg(start_at)::timestamptz IS NULL OR (CASE @time_field::text
          WHEN 'updated_at' THEN a.archived_at WHEN 'mentioned_at' THEN a.mentioned_at
          WHEN 'occurred_start' THEN a.occurred_start WHEN 'occurred_end' THEN a.occurred_end
          ELSE a.created_at END) >= sqlc.narg(start_at)::timestamptz)
      AND (sqlc.narg(end_at)::timestamptz IS NULL OR (CASE @time_field::text
          WHEN 'updated_at' THEN a.archived_at WHEN 'mentioned_at' THEN a.mentioned_at
          WHEN 'occurred_start' THEN a.occurred_start WHEN 'occurred_end' THEN a.occurred_end
          ELSE a.created_at END) < sqlc.narg(end_at)::timestamptz)
      AND (CASE @tags_match::text
          WHEN 'exact' THEN a.tags @> @filter_tags::text[] AND a.tags <@ @filter_tags::text[]
          WHEN 'any_strict' THEN cardinality(@filter_tags::text[]) = 0 OR a.tags && @filter_tags::text[]
          WHEN 'all_strict' THEN a.tags @> @filter_tags::text[]
          WHEN 'all' THEN cardinality(@filter_tags::text[]) = 0 OR cardinality(a.tags) = 0
              OR a.tags @> @filter_tags::text[]
          ELSE cardinality(@filter_tags::text[]) = 0 OR cardinality(a.tags) = 0 OR a.tags && @filter_tags::text[]
      END)
      AND (sqlc.narg(after_created_at)::timestamptz IS NULL
          OR (a.created_at, a.id) < (sqlc.narg(after_created_at)::timestamptz, @after_id::uuid))
    ORDER BY a.created_at DESC, a.id DESC
    LIMIT @lim::int)
)
SELECT p.id, p.tenant_id, p.bank_id, p.document_id, p.chunk_id, p.fact_type, p.text, p.context, p.metadata,
    p.tags, p.embedding, p.occurred_start, p.occurred_end, p.mentioned_at, p.timeless, p.proof_count,
    p.version, p.seq, p.consolidated_at, p.consolidation_failed_at, p.edited_at, p.created_at, p.updated_at,
    p.live::bool AS live,
    (CASE WHEN p.live THEN
        COALESCE((SELECT array_agg(e.canonical_name ORDER BY e.canonical_name COLLATE "C")
                  FROM unit_entities ue JOIN entities e ON e.id = ue.entity_id
                  WHERE ue.unit_id = p.id), '{}')
    ELSE
        COALESCE((SELECT array_agg(s.elem ->> 'name' ORDER BY (s.elem ->> 'name') COLLATE "C")
                  FROM jsonb_array_elements(p.entities_snapshot) AS s(elem)), '{}')
    END)::text[] AS entity_names,
    (CASE WHEN p.live THEN
        COALESCE((SELECT array_agg(ev.unit_id ORDER BY ev.unit_id)
                  FROM observation_evidence ev WHERE ev.observation_id = p.id), '{}')
    ELSE '{}' END)::uuid[] AS source_ids,
    (p.live AND p.fact_type = 'observation' AND EXISTS (
        SELECT 1 FROM memory_units f
        WHERE f.tenant_id = p.tenant_id AND f.bank_id = p.bank_id AND f.fact_type <> 'observation'
          AND f.consolidated_at IS NULL AND f.consolidation_failed_at IS NULL AND f.tags @> p.tags))::bool AS stale
FROM picked p
ORDER BY p.created_at DESC, p.id DESC, p.live DESC
LIMIT @lim::int;
