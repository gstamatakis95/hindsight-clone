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
