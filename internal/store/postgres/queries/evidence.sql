-- EvidenceObservationBank returns the tenant and bank of a live observation.
-- name: EvidenceObservationBank :one
SELECT tenant_id, org_id, bank_id FROM memory_units WHERE id = @id AND fact_type = 'observation';

-- EvidenceAdd records an observation's evidence, skipping existing (observation, unit) pairs.
-- name: EvidenceAdd :exec
INSERT INTO observation_evidence (tenant_id, org_id, bank_id, observation_id, unit_id, quote)
SELECT @tenant_id::text, @org_id::text, @bank_id::text, @observation_id::uuid, x.unit_id, (@quotes::text[])[x.i]
FROM unnest(@unit_ids::uuid[]) WITH ORDINALITY AS x(unit_id, i)
ON CONFLICT (observation_id, unit_id) DO NOTHING;

-- EvidenceCites reports whether an observation of the bank in the scope cites the unit.
-- name: EvidenceCites :one
SELECT EXISTS (
    SELECT 1 FROM observation_evidence ev JOIN memory_units o ON o.id = ev.observation_id
    WHERE ev.unit_id = @unit_id AND o.tenant_id = @tenant_id AND o.org_id = @org_id AND o.bank_id = @bank_id
      AND o.fact_type = 'observation' AND o.scope_key = @scope_key::text
);

-- EvidenceCiting locks, in id order, the bank's observations citing any of the units and returns their ids.
-- name: EvidenceCiting :many
SELECT o.id FROM memory_units o
WHERE o.tenant_id = @tenant_id AND o.org_id = @org_id AND o.bank_id = @bank_id AND o.fact_type = 'observation'
  AND EXISTS (SELECT 1 FROM observation_evidence ev WHERE ev.observation_id = o.id AND ev.unit_id = ANY(@ids::uuid[]))
ORDER BY o.id FOR UPDATE;

-- EvidenceOtherSources returns the distinct units the observations cite, other than the excluded ones.
-- name: EvidenceOtherSources :many
SELECT DISTINCT ev.unit_id FROM observation_evidence ev
WHERE ev.observation_id = ANY(@observation_ids::uuid[]) AND ev.unit_id <> ALL(@excluded::uuid[])
ORDER BY ev.unit_id;

-- EvidenceResetSources clears consolidated_at on the bank's units, so they are consolidated again.
-- name: EvidenceResetSources :exec
UPDATE memory_units SET consolidated_at = NULL
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = ANY(@ids::uuid[]);
