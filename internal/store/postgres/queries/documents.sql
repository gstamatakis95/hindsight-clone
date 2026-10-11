-- DocumentGet returns one document of a bank.
-- name: DocumentGet :one
SELECT * FROM documents WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;

-- DocumentUnitCounts counts a document's live units per fact type.
-- name: DocumentUnitCounts :many
SELECT fact_type, count(*)::bigint AS n
FROM memory_units
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id
GROUP BY fact_type;

-- DocumentUpsert inserts a document or replaces its tags, overrides, observation scopes and blob key; either
-- way extracted_at is cleared and the hashes of the last completed retain are kept.
-- name: DocumentUpsert :exec
INSERT INTO documents AS d (tenant_id, bank_id, id, overrides, tags, blob_key, observation_scopes, extracted_at)
VALUES (@tenant_id, @bank_id, @id, @overrides, @tags, @blob_key, @observation_scopes, NULL)
ON CONFLICT (tenant_id, bank_id, id) DO UPDATE
SET overrides = EXCLUDED.overrides, tags = EXCLUDED.tags, blob_key = EXCLUDED.blob_key,
    observation_scopes = EXCLUDED.observation_scopes, extracted_at = NULL, updated_at = now();

-- DocumentComplete stamps the hashes and extracted_at of a finished retain.
-- name: DocumentComplete :execrows
UPDATE documents
SET content_hash = @content_hash, overrides_hash = @overrides_hash, extracted_at = now(), updated_at = now()
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;

-- DocumentSetTags replaces a document's tag set.
-- name: DocumentSetTags :execrows
UPDATE documents SET tags = @tags, updated_at = now()
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;
