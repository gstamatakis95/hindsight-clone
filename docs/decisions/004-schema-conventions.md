# 004 — Schema conventions for the alembic revisions

## Context

Alembic owns the schema and Go never runs DDL (SPEC "File structure"). sqlc reads the pg_dump
in `schema/schema.sql`, so every choice below shows up directly in the generated Go types.
The proto messages (Bank, Document, Memory, Chunk, MentalModel, Operation, LifecycleEvent,
Directive, ObservationVersion, MentalModelHistoryEntry) decide which columns exist.

## Decision

**Tenancy and keys**

- Every table has `tenant_id text NOT NULL` (SPEC "Decisions: Tenancy"). The exception is
  `tenants`, whose `id` is the tenant id. `tenants` only holds config: banks have no foreign
  key to it, because a write to an unknown bank creates the bank with tenant defaults and no
  tenant row is required.
- Client-supplied ids (bank, document, mental model, directive) are `text`, unique per
  `(tenant_id, bank_id)`: composite primary keys `(tenant_id, bank_id, id)` (banks:
  `(tenant_id, id)`).
- Server-generated ids (memory units, chunks, entities, operations) are `uuid` primary keys,
  globally unique, `DEFAULT gen_random_uuid()` as a fallback (Go may supply UUIDv7). pg_search
  needs one unique key column per BM25 index, which a composite key can't provide.
- Child tables of banks reference `banks (tenant_id, id) ON DELETE CASCADE`. Tables keyed by a
  uuid parent (unit_entities, memory_links, observation_evidence, entity_cooccurrences)
  reference that parent and carry `tenant_id, bank_id` denormalised, without a bank FK.

**Types**

- Timestamps are `timestamptz`; `created_at` / `updated_at` are `NOT NULL DEFAULT now()`.
  Nullable timestamps mean "not happened yet" (`extracted_at`, `consolidated_at`, ...).
- Proto `string` fields are `text NOT NULL DEFAULT ''` unless NULL carries meaning
  (`memory_units.document_id`: NULL = observation / no document; `serial_key`: NULL = no
  key; `operations.error`: NULL = no error; `label_value`: NULL = not a label entity).
- Tags are normalised `text[] NOT NULL DEFAULT '{}'` with GIN indexes. Config, overrides,
  triggers, progress, usage and snapshots are `jsonb` (proto JSON). `usage` defaults to `'[]'`.
- Enum-like columns are `text` with a `CHECK (... IN (...))`, using lower snake case of the proto
  enum value without its prefix (`fact_type`, `operations.kind/status`, `memory_links.kind`,
  `mental_model_history.kind`, `memory_audit.action`, `parked_links.direction`). Adding a value
  needs a new revision. `lifecycle_events.type` deliberately has no CHECK so new event types
  don't need a migration.
- History-style tables (`mental_model_history`, `lifecycle_events`, `memory_audit`,
  `observation_versions`) use `bigserial` ids; `id` orders rows with equal timestamps or seq.

**Specific choices**

- `operations.attempts` (not `attempt`) matches proto `Operation.attempts`.
- `operations.parent_id` has no foreign key: pruning terminal operations must never cascade
  into, or be blocked by, other rows. Neither `operations` nor `lifecycle_events` references
  banks: the delete_bank job and the event history outlive the bank row.
- `lifecycle_events.seq` is not unique: events written in one transaction share the bank's
  change_seq.
- `chunks.truncated` exists because proto `Chunk.truncated` does.
- `memory_links` has `CHECK (from_unit_id <> to_unit_id)`: a unit never links to itself, so
  writers must filter self-pairs (e.g. from semantic neighbour searches) before inserting.
- `memory_units_archive` also keeps `chunk_id` and `edited_at`, because ListMemories returns
  `Memory` (which has both) from the live and the archive table alike.
- `memory_units.scope_key` is set only for observations
  (`CHECK (scope_key IS NULL OR fact_type = 'observation')`); the converse is not enforced, so
  the shared scope can use whatever encoding `ScopeKey.String()` gives an empty tag set.
- `memory_units.chunk_id` is `ON DELETE SET NULL`; `document_id` cascades through
  `(tenant_id, bank_id, document_id) -> documents` (MATCH SIMPLE, so NULL skips the check).
- `entities` has no unique name: resolution may keep two entities with the same name. Label
  entities are unique on `(tenant_id, bank_id, kind, label_value) WHERE label_value IS NOT NULL`.
- `graph_dirty`, `entity_maintenance`, `parked_links`, `memory_units_archive`, `memory_audit`
  and `observation_versions` have no foreign keys: maintenance tolerates vanished rows and
  history outlives what it describes. delete_bank must delete these rows explicitly.
- `memory_units_archive` stores no embedding: restore re-embeds through rederive_memory.
- `mental_models.folder_path` has no FK to `mental_model_folders`: `''` is the root and folders
  can be implicit.
- `mental_models.refresh_cron` duplicates `trigger.refresh_cron` so the cron tick can use a
  partial index; writers keep the two in sync.

**Indexes**

- Constraint-like unique indexes live with their table (e.g. `entities_label_uniq` in 0003).
  Retrieval, claim and supporting indexes are in 0008; 0009 creates its own tables' indexes.
- BM25 (`memory_units_bm25`): `USING bm25 (id, text, context, (tenant_id::pdb.literal),
  (bank_id::pdb.literal), (fact_type::pdb.literal))`. pg_search 0.26 deprecated `key_field`
  (a no-op that only warns), so the option is omitted; the uuid `id` is listed first, following
  ParadeDB's convention for the key column. Filter columns use
  the literal tokenizer so equality filters are pushed into the BM25 scan. `text` and
  `context` match what Hindsight indexes. Score with `paradedb.score(id)`; match with `|||`
  (or `@@@`). Only one bm25 index per table is allowed.
- Mental models have no BM25 index (composite text key; would need a surrogate uuid). Page
  search uses `mental_models_embedding_hnsw`.
- HNSW indexes use `vector_cosine_ops` with default `m` / `ef_construction`.
- The reclaim index is `operations (heartbeat_at) WHERE status = 'processing'` rather than
  `(status, heartbeat_at)`: the predicate already fixes status, so the column would add nothing.
- Unique `operations (tenant_id, bank_id, serial_key) WHERE status = 'processing'` backs the
  claim; NULL serial keys never conflict.

## Alternatives

- **Composite uuid keys `(tenant_id, bank_id, id)`:** rejected; pg_search needs a single key
  column and global uuids already make cross-tenant collisions impossible.
- **Postgres enums instead of CHECKs:** enum changes can't run inside a transaction on older
  versions and are awkward in sqlc; CHECKs drop and re-add cleanly.
- **Nullable text for every proto string:** generates `pgtype.Text` everywhere for no benefit.

## Spec lines involved

- SPEC.md 912-1017, "File structure": the migrations list (948-965), `schema/schema.sql` (967) and
  1017 (alembic owns the schema, raw SQL via `op.execute()`).
- SPEC.md 1089-1314, "Ports": `Seq` (1124), `BankSeq` (1144), `UnitRepo.Archive` (1162), EventRepo,
  Claimer.
- SPEC.md 1903-1955, "Jobs": the claim SQL and the unique processing index (1928).
- SPEC.md 1957-1971, "Curation": archive, audit, observation_versions.
- SPEC.md 1973-2015, "Behavioural rules": unknown bank creation (1979), DeleteBank (1980), change
  sequence (1984).
- SPEC.md 2030-2041, "Decisions": embedding (2032), keyword search (2036), tenancy (2040).
- Proto messages: `Bank` 347, `Directive` 372, `Memory` 518, `Chunk` 538, `ObservationVersion` 635,
  `Document` 658, `RefreshTrigger` 707, `MentalModelSpec` 727, `MentalModel` 737,
  `MentalModelHistoryEntry` 815, `Operation` 847, `LifecycleEvent` 884.
