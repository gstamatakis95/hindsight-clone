# 140 — ChunkRepo.Insert replaces a chunk by deleting the old row

Status: accepted (WP-01)

## Context

`ChunkRepo.Insert` says "a chunk at an already committed (document, index) replaces it". The schema has
`chunks_pkey (id)`, `UNIQUE (tenant_id, bank_id, document_id, chunk_index)` and
`memory_units.chunk_id -> chunks(id) ON DELETE SET NULL` with the default ON UPDATE NO ACTION. The caller
picks the new chunk's id and stamps the units it writes next with it.

## Decision

- Insert first deletes any other chunk at the same (tenant, bank, document, index) (`id <> new id`), then
  inserts the new chunk under its own id. Units of the replaced chunk keep living with `chunk_id = NULL`
  (provenance only; the retain that replaces a chunk replaces the document's units anyway).
- Re-inserting the same chunk id (a replayed job) rewrites text, hash and token count in place, so units
  already pointing at it keep their `chunk_id`.
- An unknown document is a foreign-key violation, mapped to `domain.ErrNotFound`.

## Alternatives

- `ON CONFLICT (document, index) DO UPDATE` keeping the old id: units the caller writes with the new id
  would then violate the chunk_id foreign key.
- `ON CONFLICT ... DO UPDATE SET id = EXCLUDED.id`: fails (ON UPDATE NO ACTION) whenever units still
  reference the old chunk.

## Spec lines involved

- SPEC.md 1981 (retain idempotency: an interrupted retain resumes, skipping chunks committed with a
  matching hash); `ChunkRepo` doc in `internal/port/repos.go`; decision 004 (`memory_units.chunk_id` is
  ON DELETE SET NULL).
