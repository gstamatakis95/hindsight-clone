-- ChunkGetMany returns the bank's chunks with the given ids.
-- name: ChunkGetMany :many
SELECT * FROM chunks
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[]);

-- EntityStatesOf returns, ordered by id, the entities attached to the bank's live units with the given ids, each with the sorted live observations attached to it or citing a live fact attached to it.
-- name: EntityStatesOf :many
SELECT e.id, e.canonical_name,
    COALESCE(ARRAY(
        SELECT z.id FROM (
            SELECT o.id FROM unit_entities x JOIN memory_units o ON o.id = x.unit_id
            WHERE x.entity_id = e.id AND o.fact_type = 'observation'
              AND o.tenant_id = @tenant_id AND o.org_id = @org_id AND o.bank_id = @bank_id
            UNION
            SELECT o.id FROM unit_entities x
            JOIN memory_units f ON f.id = x.unit_id AND f.fact_type <> 'observation'
            JOIN observation_evidence ev ON ev.unit_id = f.id
            JOIN memory_units o ON o.id = ev.observation_id AND o.fact_type = 'observation'
            WHERE x.entity_id = e.id AND o.tenant_id = @tenant_id AND o.org_id = @org_id AND o.bank_id = @bank_id
        ) AS z ORDER BY z.id
    ), '{}')::uuid[] AS observation_ids
FROM entities e
WHERE e.tenant_id = @tenant_id AND e.org_id = @org_id AND e.bank_id = @bank_id AND e.id IN (
    SELECT ue.entity_id FROM unit_entities ue JOIN memory_units u ON u.id = ue.unit_id
    WHERE u.tenant_id = @tenant_id AND u.org_id = @org_id AND u.bank_id = @bank_id AND u.id = ANY(@ids::uuid[])
)
ORDER BY e.id;

-- ScopeUnconsolidated counts, for each tag set in scopes (a JSON array of arrays), the bank's unconsolidated facts (CCR §0) whose tags contain it; ord is the tag set's 1-based position. It is the one scope-wise rendering of the §0 predicate (StaleScopes and ListObservationScopes use it), written exactly like the partial index memory_units_unconsolidated_idx so the planner serves it from there.
-- name: ScopeUnconsolidated :many
SELECT s.ord::int AS ord, (
    SELECT count(*) FROM memory_units u
    WHERE u.tenant_id = @tenant_id AND u.org_id = @org_id AND u.bank_id = @bank_id
      AND u.consolidated_at IS NULL AND u.consolidation_failed_at IS NULL AND u.fact_type <> 'observation'
      AND u.tags @> ARRAY(SELECT jsonb_array_elements_text(s.tags))
)::bigint AS n
FROM jsonb_array_elements(@scopes::jsonb) WITH ORDINALITY AS s(tags, ord)
ORDER BY s.ord;

-- ScopeObservationCount counts the bank's live observations whose scope key is the given one.
-- name: ScopeObservationCount :one
SELECT count(*) FROM memory_units
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND fact_type = 'observation' AND scope_key = @scope_key::text;

-- ScopeListPage returns up to lim of the bank's observation scopes with a key strictly after after ("" = from the start, so the shared scope "" lists), in byte order, each with its live observation count.
-- name: ScopeListPage :many
SELECT s.scope_key, s.tags, (
    SELECT count(*) FROM memory_units o
    WHERE o.tenant_id = s.tenant_id AND o.org_id = s.org_id AND o.bank_id = s.bank_id AND o.fact_type = 'observation'
      AND o.scope_key = s.scope_key
)::bigint AS observations
FROM observation_scopes s
WHERE s.tenant_id = @tenant_id AND s.org_id = @org_id AND s.bank_id = @bank_id
  AND (@after::text = '' OR s.scope_key COLLATE "C" > @after::text)
ORDER BY s.scope_key COLLATE "C"
LIMIT @lim;

-- ModelsCitingUnits returns, in byte order, the ids of the bank's mental models whose source_ids include any of the units.
-- name: ModelsCitingUnits :many
SELECT id FROM mental_models
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND source_ids && @ids::uuid[]
ORDER BY id COLLATE "C";

-- EvidenceCitingScopes returns, in byte order, the distinct scope keys of the bank's live observations citing any of the units.
-- name: EvidenceCitingScopes :many
SELECT o.scope_key::text AS scope_key FROM memory_units o
WHERE o.tenant_id = @tenant_id AND o.org_id = @org_id AND o.bank_id = @bank_id AND o.fact_type = 'observation'
  AND EXISTS (SELECT 1 FROM observation_evidence ev WHERE ev.observation_id = o.id AND ev.unit_id = ANY(@ids::uuid[]))
GROUP BY o.scope_key
ORDER BY o.scope_key COLLATE "C";

-- EvidenceLockScopes locks, in id order, the bank's live observations whose scope key is one of keys and returns their ids.
-- name: EvidenceLockScopes :many
SELECT id FROM memory_units
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND fact_type = 'observation' AND scope_key = ANY(@keys::text[])
ORDER BY id FOR UPDATE;

-- EvidenceLockAll locks, in id order, every live observation of the bank and returns their ids.
-- name: EvidenceLockAll :many
SELECT id FROM memory_units
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND fact_type = 'observation'
ORDER BY id FOR UPDATE;
