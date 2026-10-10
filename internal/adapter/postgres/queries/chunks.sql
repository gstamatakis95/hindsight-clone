-- ChunkCommitted returns a document's committed chunks as (index, content hash).
-- name: ChunkCommitted :many
SELECT chunk_index, content_hash FROM chunks
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id;

-- ChunkDeleteReplaced deletes another chunk at the same (document, index), so the new one can replace it.
-- name: ChunkDeleteReplaced :exec
DELETE FROM chunks
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND document_id = @document_id
  AND chunk_index = @chunk_index AND id <> @id;

-- ChunkInsert stores one chunk; a replayed insert of the same id rewrites it in place.
-- name: ChunkInsert :exec
INSERT INTO chunks (id, tenant_id, bank_id, document_id, chunk_index, text, content_hash, token_count, created_at)
VALUES (
    @id, @tenant_id, @bank_id, @document_id, @chunk_index, @text, @content_hash, @token_count,
    COALESCE(sqlc.narg(created_at)::timestamptz, now())
)
ON CONFLICT (id) DO UPDATE
SET text = EXCLUDED.text, content_hash = EXCLUDED.content_hash, token_count = EXCLUDED.token_count;
