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
  banks. They do not outlive the bank: delete_bank's final transaction deletes the bank's
  remaining `lifecycle_events` and `operations` rows (its own row included) with explicit
  DELETEs, together with the bank row. The runner's terminal Complete for that job must
  therefore accept that the row is gone, and GetOperation on it afterwards returns NotFound.
- Enqueue on a tombstoned bank (`deleting_at` set) fails with ErrBankDeleting, except for a
  delete_bank job.
- `lifecycle_events.type` is free text and `operation_id` is a nullable uuid, so terminal
  failures (OPERATION_FAILED, written by Fail / ReclaimStale) need nothing beyond the table.
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
- Every foreign key states its ON DELETE action; none is NO ACTION. The full list:

  | Foreign key | References | ON DELETE | Why |
  |---|---|---|---|
  | `documents`, `entities`, `memory_units`, `observation_scopes`, `mental_model_folders`, `mental_models`, `directives` `(tenant_id, bank_id)` | `banks` | CASCADE | Bank-owned rows go with the bank row |
  | `chunks (tenant_id, bank_id, document_id)` | `documents` | CASCADE | Chunks belong to their document |
  | `memory_units (tenant_id, bank_id, document_id)` | `documents` | CASCADE | Deleting a document deletes its units |
  | `memory_units.chunk_id` | `chunks` | SET NULL | Deleting a chunk keeps its units; `chunk_id` is provenance only |
  | `unit_entities.unit_id`, `unit_entities.entity_id` | `memory_units`, `entities` | CASCADE | A join row is meaningless without either side |
  | `entity_cooccurrences.entity_a`, `.entity_b` | `entities` | CASCADE | Same |
  | `memory_links.from_unit_id`, `.to_unit_id` | `memory_units` | CASCADE | Same (invalidate snapshots causal links before the unit leaves) |
  | `observation_evidence.observation_id`, `.unit_id` | `memory_units` | CASCADE | Evidence goes when either the observation or its source unit is deleted or archived |
  | `mental_model_history (tenant_id, bank_id, mental_model_id)` | `mental_models` | CASCADE | History belongs to its model |
- `entities` has no unique name: resolution may keep two entities with the same name. Label
  entities are unique on `(tenant_id, bank_id, kind, label_value) WHERE label_value IS NOT NULL`.
- `graph_dirty`, `entity_maintenance`, `parked_links`, `memory_units_archive`, `memory_audit`
  and `observation_versions` have no foreign keys: maintenance tolerates vanished rows and
  history outlives the units it describes (not the bank). delete_bank must delete these rows
  explicitly.
- `parked_links` mirrors `memory_links`: `kind IN ('temporal', 'semantic', 'causal')` and
  `archived_unit_id <> other_unit_id` (`parked_links_kind`, `parked_links_no_self_link`).
- `memory_units_archive` stores no embedding: restore re-embeds through rederive_memory.
- `mental_models.folder_path` has no FK to `mental_model_folders`: `''` is the root and folders
  can be implicit.
- `mental_models.refresh_cron` duplicates `trigger.refresh_cron` so the cron tick can use a
  partial index; writers keep the two in sync.

**Operations: dedupe, cancel and the claim**

- `operations.joinable boolean NOT NULL DEFAULT false`. Enqueue sets it true only for a row
  inserted under a dedupe policy (Pending or PendingOrRunning) without overrides, except
  refresh_mental_model rows, which are always joinable. Dedupe lookups match joinable rows
  only, so a row enqueued with overrides (or with no dedupe policy) is never joined. The
  schema doesn't encode this policy (no CHECK on kind or overrides); Go owns it.
- Dedupe Enqueue is race-safe without a leader: before its dedupe SELECT, and in the same
  transaction as the INSERT, the Postgres adapter takes
  `pg_advisory_xact_lock(hashtextextended(tenant_id || '/' || bank_id || '/' || kind || '/' || serial_key, 0))`,
  so two concurrent enqueuers of the same (tenant, bank, kind, serial key) never both insert.
  Dedupe enqueues always carry a serial key (a NULL key would hash to NULL and take no lock).
  Hash collisions only serialize unrelated enqueues, which is harmless.
- `finished_at` is set only on terminal rows
  (`operations_finished_at_terminal_only CHECK (finished_at IS NULL OR status IN ('completed', 'failed', 'cancelled'))`),
  so Retry must clear it when it re-queues a row. Terminal rows are not required to have it
  set: a cancelled row with `finished_at` NULL is still running.
- Cancel on a PENDING row sets `status = 'cancelled'`, `finished_at = now()`. Cancel on a
  PROCESSING row sets `status = 'cancelled'` and leaves `finished_at` NULL: the row keeps
  holding its serial key until the handler stops. The handler's terminal Complete / Fail
  then sets only `finished_at` (status stays `cancelled`); ReclaimStale sets `finished_at`
  on cancelled-but-unfinished rows whose heartbeat is stale.
- Prune deletes terminal rows with `finished_at < cutoff`, so it never removes a row that
  still holds its key.
- The claim is the spec's SQL plus one clause: a cancelled-but-unfinished row holds the key.
  `joinable` plays no part in it.

  ```sql
  SELECT o.* FROM operations o
  JOIN banks b ON (b.tenant_id, b.id) = (o.tenant_id, o.bank_id)
  WHERE o.status = 'pending' AND o.run_at <= now() AND o.kind = ANY($1)
    AND (b.deleting_at IS NULL OR o.kind = 'delete_bank')
    AND NOT EXISTS (
      SELECT 1 FROM operations p
      WHERE (p.tenant_id, p.bank_id, p.serial_key) = (o.tenant_id, o.bank_id, o.serial_key)
        AND (p.status = 'processing'
             OR (p.status = 'cancelled' AND p.finished_at IS NULL)
             OR (p.status = 'pending' AND p.run_at <= now()
                 AND (p.created_at, p.id) < (o.created_at, o.id))))
  ORDER BY o.created_at
  LIMIT $2
  FOR UPDATE OF o SKIP LOCKED;
  -- then: UPDATE operations SET status = 'processing', worker_id = $3, heartbeat_at = now() WHERE id = ANY(...)
  ```

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
- The unconsolidated index (`memory_units_unconsolidated_idx`) has the predicate
  `consolidated_at IS NULL AND consolidation_failed_at IS NULL AND fact_type <> 'observation'`:
  observations are consolidation's output and are never consolidated themselves.
- `operations_active_serial_key_idx` is `(tenant_id, bank_id, serial_key, kind) WHERE status
  IN ('pending', 'processing') OR (status = 'cancelled' AND finished_at IS NULL)`: every row
  that holds or queues a key. The claim's NOT EXISTS uses its first three columns, and the
  dedupe lookup uses all four (with `joinable` as a filter), so no separate dedupe index is
  needed. Both plans were checked with EXPLAIN.
- The reclaim index `operations_running_heartbeat_idx` is `(heartbeat_at) WHERE status =
  'processing' OR (status = 'cancelled' AND finished_at IS NULL)`, so ReclaimStale finds stale
  cancelled-but-running rows too. Status isn't a column: the predicate already fixes it.
- Unique `operations (tenant_id, bank_id, serial_key) WHERE status = 'processing'` backs the
  claim, exactly as in the spec; NULL serial keys never conflict. It isn't widened to
  cancelled rows: Cancel only turns the one processing holder into a cancelled holder, and the
  claim's NOT EXISTS already waits on cancelled holders.
- `parked_links_archived_unit_idx (tenant_id, bank_id, archived_unit_id)` serves restore and
  bank-scoped deletes; `parked_links_other_unit_idx (other_unit_id)` serves deletes of the
  other unit.

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
- SPEC.md 1903-1955, "Jobs": the claim SQL (amended above for cancelled-but-running rows),
  dedupe policies and the unique processing index (1928).
- SPEC.md 1957-1971, "Curation": archive, audit, observation_versions.
- SPEC.md 1973-2015, "Behavioural rules": unknown bank creation (1979), DeleteBank (1980), change
  sequence (1984).
- SPEC.md 2030-2041, "Decisions": embedding (2032), keyword search (2036), tenancy (2040).
- Proto messages: `Bank` 347, `Directive` 372, `Memory` 518, `Chunk` 538, `ObservationVersion` 635,
  `Document` 658, `RefreshTrigger` 707, `MentalModelSpec` 727, `MentalModel` 737,
  `MentalModelHistoryEntry` 815, `Operation` 847, `LifecycleEvent` 884.
