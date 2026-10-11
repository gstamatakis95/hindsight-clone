"""Baseline: memd's whole Postgres schema.

Until the first release this is the only revision and is edited in place; after it, schema
changes are new revisions on top of this one.

Areas, in creation order:

- extensions: vector (768-dim embeddings, HNSW), pg_trgm (fuzzy entity names), pg_search
  (BM25 keyword recall);
- tenants and banks;
- documents and chunks;
- memory_units: raw facts (world, experience) and observations in one table;
- graph: entities, unit_entities, entity_cooccurrences, memory_links and the graph
  maintenance queues;
- observations: evidence, scopes and version history;
- curation: the archive of invalidated units and the audit log;
- mental models and their history;
- directives;
- jobs: operations (the queue) and lifecycle_events.

Conventions: every table but tenants carries ``tenant_id``. Bank-owned tables cascade from
banks, directly or through their parent row; the tables without that foreign key (the graph
queues, observation_versions, the curation tables, operations and lifecycle_events) are
cleared explicitly by bank delete. Memory units carry the bank ``change_seq`` of the
transaction that last wrote them.

Revision ID: 0001
Revises:
Create Date: 2026-10-11
"""

from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None

EXTENSIONS = [
    "CREATE EXTENSION IF NOT EXISTS vector",
    "CREATE EXTENSION IF NOT EXISTS pg_trgm",
    "CREATE EXTENSION IF NOT EXISTS pg_search",
]

TENANTS_AND_BANKS = [
    """
    CREATE TABLE tenants (
        id         text        PRIMARY KEY,
        config     jsonb       NOT NULL DEFAULT '{}',
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    )
    """,
    """
    CREATE TABLE banks (
        tenant_id            text        NOT NULL,
        id                   text        NOT NULL,
        name                 text        NOT NULL DEFAULT '',
        config               jsonb       NOT NULL DEFAULT '{}',
        -- Bumped by every transaction that changes the bank's memory; rows are stamped with it.
        change_seq           bigint      NOT NULL DEFAULT 0,
        -- Delete tombstone: set when bank deletion starts.
        deleting_at          timestamptz NULL,
        last_memory_write_at timestamptz NULL,
        created_at           timestamptz NOT NULL DEFAULT now(),
        updated_at           timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, id)
    )
    """,
]

DOCUMENTS = [
    """
    CREATE TABLE documents (
        tenant_id      text        NOT NULL,
        bank_id        text        NOT NULL,
        id             text        NOT NULL,
        content_hash   text        NOT NULL DEFAULT '',
        -- Hash of the overrides of the last completed retain.
        overrides_hash text        NOT NULL DEFAULT '',
        overrides      jsonb       NULL,
        tags           text[]      NOT NULL DEFAULT '{}',
        -- Object-store key of the original text.
        blob_key       text        NOT NULL DEFAULT '',
        extracted_at   timestamptz NULL,
        created_at     timestamptz NOT NULL DEFAULT now(),
        updated_at     timestamptz NOT NULL DEFAULT now(),
        -- The retain item's observation scope modes; NULL = the item set none.
        observation_scope_modes jsonb NULL,
        PRIMARY KEY (tenant_id, bank_id, id),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
    )
    """,
    "CREATE INDEX documents_tags_gin ON documents USING gin (tags)",
    """
    CREATE TABLE chunks (
        id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id    text        NOT NULL,
        bank_id      text        NOT NULL,
        document_id  text        NOT NULL,
        chunk_index  integer     NOT NULL,
        text         text        NOT NULL,
        content_hash text        NOT NULL DEFAULT '',
        token_count  integer     NOT NULL DEFAULT 0,
        truncated    boolean     NOT NULL DEFAULT false,
        created_at   timestamptz NOT NULL DEFAULT now(),
        UNIQUE (tenant_id, bank_id, document_id, chunk_index),
        FOREIGN KEY (tenant_id, bank_id, document_id)
            REFERENCES documents (tenant_id, bank_id, id) ON DELETE CASCADE
    )
    """,
]

MEMORY_UNITS = [
    """
    CREATE TABLE memory_units (
        id                      uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id               text        NOT NULL,
        bank_id                 text        NOT NULL,
        document_id             text        NULL,
        chunk_id                uuid        NULL REFERENCES chunks (id) ON DELETE SET NULL,
        fact_type               text        NOT NULL
            CHECK (fact_type IN ('world', 'experience', 'observation')),
        text                    text        NOT NULL,
        context                 text        NOT NULL DEFAULT '',
        metadata                jsonb       NOT NULL DEFAULT '{}',
        tags                    text[]      NOT NULL DEFAULT '{}',
        embedding               vector(768) NULL,
        occurred_start          timestamptz NULL,
        occurred_end            timestamptz NULL,
        mentioned_at            timestamptz NULL,
        timeless                boolean     NOT NULL DEFAULT false,
        -- Observations only: how many source facts support it.
        proof_count             integer     NOT NULL DEFAULT 0,
        -- Observations only: the canonical tag set (scope) the observation belongs to.
        scope_key               text        NULL,
        version                 bigint      NOT NULL DEFAULT 1,
        -- The bank change_seq of the transaction that last wrote the row.
        seq                     bigint      NOT NULL,
        -- Raw facts: set once consolidation has processed the fact, or given up on it.
        consolidated_at         timestamptz NULL,
        consolidation_failed_at timestamptz NULL,
        edited_at               timestamptz NULL,
        created_at              timestamptz NOT NULL DEFAULT now(),
        updated_at              timestamptz NOT NULL DEFAULT now(),
        CONSTRAINT memory_units_scope_key_observation_only
            CHECK (scope_key IS NULL OR fact_type = 'observation'),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE,
        FOREIGN KEY (tenant_id, bank_id, document_id)
            REFERENCES documents (tenant_id, bank_id, id) ON DELETE CASCADE
    )
    """,
    # Retrieval arms: semantic (HNSW), keyword (BM25), tags, temporal.
    "CREATE INDEX memory_units_embedding_hnsw ON memory_units USING hnsw (embedding vector_cosine_ops)",
    # pg_search: the uuid id comes first (ParadeDB's key-column convention; key_field is a no-op since
    # 0.26). The pdb.literal columns make the tenant / bank / fact type filters exact and pushed into the scan.
    """
    CREATE INDEX memory_units_bm25 ON memory_units USING bm25 (
        id, text, context,
        (tenant_id::pdb.literal), (bank_id::pdb.literal), (fact_type::pdb.literal))
    """,
    "CREATE INDEX memory_units_tags_gin ON memory_units USING gin (tags)",
    "CREATE INDEX memory_units_occurred_start_idx ON memory_units (tenant_id, bank_id, occurred_start)",
    # Facts waiting for consolidation. Observations are its output and are never consolidated.
    """
    CREATE INDEX memory_units_unconsolidated_idx ON memory_units (tenant_id, bank_id, created_at)
        WHERE consolidated_at IS NULL AND consolidation_failed_at IS NULL AND fact_type <> 'observation'
    """,
    """
    CREATE INDEX memory_units_observation_scope_idx ON memory_units (tenant_id, bank_id, scope_key)
        WHERE fact_type = 'observation'
    """,
    # Staleness checks (newest seq in a scope), document and chunk cascades, listings.
    "CREATE INDEX memory_units_seq_idx ON memory_units (tenant_id, bank_id, seq)",
    "CREATE INDEX memory_units_document_idx ON memory_units (tenant_id, bank_id, document_id)",
    "CREATE INDEX memory_units_created_at_idx ON memory_units (tenant_id, bank_id, created_at)",
    "CREATE INDEX memory_units_chunk_idx ON memory_units (chunk_id) WHERE chunk_id IS NOT NULL",
]

GRAPH = [
    # Entity names are not unique: resolution may keep two entities with the same name.
    """
    CREATE TABLE entities (
        id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id      text        NOT NULL,
        bank_id        text        NOT NULL,
        canonical_name text        NOT NULL,
        kind           text        NOT NULL DEFAULT '',
        -- Set only for label entities, which are unique per (bank, kind, label_value).
        label_value    text        NULL,
        mention_count  integer     NOT NULL DEFAULT 0,
        first_seen     timestamptz NULL,
        last_seen      timestamptz NULL,
        created_at     timestamptz NOT NULL DEFAULT now(),
        updated_at     timestamptz NOT NULL DEFAULT now(),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
    )
    """,
    """
    CREATE UNIQUE INDEX entities_label_uniq
        ON entities (tenant_id, bank_id, kind, label_value)
        WHERE label_value IS NOT NULL
    """,
    # Fuzzy (trigram) and exact case-insensitive name lookup.
    "CREATE INDEX entities_name_trgm ON entities USING gin (lower(canonical_name) gin_trgm_ops)",
    "CREATE INDEX entities_name_idx ON entities (tenant_id, bank_id, lower(canonical_name))",
    """
    CREATE TABLE unit_entities (
        tenant_id text NOT NULL,
        bank_id   text NOT NULL,
        unit_id   uuid NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
        entity_id uuid NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
        PRIMARY KEY (unit_id, entity_id)
    )
    """,
    "CREATE INDEX unit_entities_entity_idx ON unit_entities (entity_id)",
    # One row per unordered entity pair, stored with entity_a < entity_b.
    """
    CREATE TABLE entity_cooccurrences (
        tenant_id text        NOT NULL,
        bank_id   text        NOT NULL,
        entity_a  uuid        NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
        entity_b  uuid        NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
        count     integer     NOT NULL DEFAULT 0,
        last_at   timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (entity_a, entity_b),
        CONSTRAINT entity_cooccurrences_ordered CHECK (entity_a < entity_b)
    )
    """,
    "CREATE INDEX entity_cooccurrences_entity_b_idx ON entity_cooccurrences (entity_b)",
    """
    CREATE TABLE memory_links (
        tenant_id    text             NOT NULL,
        bank_id      text             NOT NULL,
        from_unit_id uuid             NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
        to_unit_id   uuid             NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
        kind         text             NOT NULL CHECK (kind IN ('temporal', 'semantic', 'causal')),
        weight       double precision NOT NULL DEFAULT 1,
        created_at   timestamptz      NOT NULL DEFAULT now(),
        PRIMARY KEY (from_unit_id, to_unit_id, kind),
        CONSTRAINT memory_links_no_self_link CHECK (from_unit_id <> to_unit_id)
    )
    """,
    "CREATE INDEX memory_links_to_unit_idx ON memory_links (to_unit_id)",
    # Graph maintenance queues: units and entities whose links or stats need recomputing. No foreign
    # keys, because maintenance tolerates rows whose unit or entity has since vanished.
    """
    CREATE TABLE graph_dirty (
        tenant_id  text        NOT NULL,
        bank_id    text        NOT NULL,
        unit_id    uuid        NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, bank_id, unit_id)
    )
    """,
    """
    CREATE TABLE entity_maintenance (
        tenant_id  text        NOT NULL,
        bank_id    text        NOT NULL,
        entity_id  uuid        NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, bank_id, entity_id)
    )
    """,
]

OBSERVATIONS = [
    # Links an observation to each source unit it cites. Both keys cascade: an evidence row is
    # meaningless once either side is deleted or archived.
    """
    CREATE TABLE observation_evidence (
        tenant_id      text        NOT NULL,
        bank_id        text        NOT NULL,
        observation_id uuid        NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
        unit_id        uuid        NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
        quote          text        NOT NULL DEFAULT '',
        created_at     timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (observation_id, unit_id)
    )
    """,
    "CREATE INDEX observation_evidence_unit_idx ON observation_evidence (unit_id)",
    # The scopes (canonical tag sets) a bank has observations in, for ListObservationScopes.
    """
    CREATE TABLE observation_scopes (
        tenant_id  text        NOT NULL,
        bank_id    text        NOT NULL,
        scope_key  text        NOT NULL,
        tags       text[]      NOT NULL DEFAULT '{}',
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, bank_id, scope_key),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
    )
    """,
    # Previous versions of each observation. No foreign key: history outlives the observation.
    """
    CREATE TABLE observation_versions (
        id             bigserial   PRIMARY KEY,
        tenant_id      text        NOT NULL,
        bank_id        text        NOT NULL,
        observation_id uuid        NOT NULL,
        version        bigint      NOT NULL,
        text           text        NOT NULL,
        source_ids     uuid[]      NOT NULL DEFAULT '{}',
        at             timestamptz NOT NULL DEFAULT now(),
        UNIQUE (observation_id, version)
    )
    """,
    "CREATE INDEX observation_versions_bank_idx ON observation_versions (tenant_id, bank_id)",
]

CURATION = [
    # Invalidated units move here with snapshots of their entities and causal links, so recall
    # never needs a state filter; restore moves them back. No foreign keys: archive and audit
    # outlive the units they describe.
    """
    CREATE TABLE memory_units_archive (
        id                uuid        PRIMARY KEY,
        tenant_id         text        NOT NULL,
        bank_id           text        NOT NULL,
        document_id       text        NULL,
        chunk_id          uuid        NULL,
        fact_type         text        NOT NULL
            CHECK (fact_type IN ('world', 'experience', 'observation')),
        text              text        NOT NULL,
        context           text        NOT NULL DEFAULT '',
        metadata          jsonb       NOT NULL DEFAULT '{}',
        tags              text[]      NOT NULL DEFAULT '{}',
        occurred_start    timestamptz NULL,
        occurred_end      timestamptz NULL,
        mentioned_at      timestamptz NULL,
        timeless          boolean     NOT NULL DEFAULT false,
        seq               bigint      NOT NULL,
        edited_at         timestamptz NULL,
        entities_snapshot jsonb       NOT NULL DEFAULT '[]',
        links_snapshot    jsonb       NOT NULL DEFAULT '[]',
        reason            text        NOT NULL DEFAULT '',
        operation_id      uuid        NULL,
        created_at        timestamptz NOT NULL DEFAULT now(),
        archived_at       timestamptz NOT NULL DEFAULT now()
    )
    """,
    "CREATE INDEX memory_units_archive_bank_idx ON memory_units_archive (tenant_id, bank_id, archived_at)",
    "CREATE INDEX memory_units_archive_document_idx ON memory_units_archive (tenant_id, bank_id, document_id)",
    """
    CREATE INDEX memory_units_archive_operation_idx
        ON memory_units_archive (operation_id) WHERE operation_id IS NOT NULL
    """,
    """
    CREATE TABLE memory_audit (
        id           bigserial   PRIMARY KEY,
        tenant_id    text        NOT NULL,
        bank_id      text        NOT NULL,
        unit_id      uuid        NOT NULL,
        action       text        NOT NULL CHECK (action IN ('edit', 'invalidate', 'restore')),
        reason       text        NOT NULL DEFAULT '',
        before       jsonb       NULL,
        after        jsonb       NULL,
        operation_id uuid        NULL,
        at           timestamptz NOT NULL DEFAULT now()
    )
    """,
    "CREATE INDEX memory_audit_unit_idx ON memory_audit (tenant_id, bank_id, unit_id, at)",
    "CREATE INDEX memory_audit_operation_idx ON memory_audit (operation_id) WHERE operation_id IS NOT NULL",
]

MENTAL_MODELS = [
    """
    CREATE TABLE mental_models (
        tenant_id              text        NOT NULL,
        bank_id                text        NOT NULL,
        id                     text        NOT NULL,
        name                   text        NOT NULL DEFAULT '',
        source_query           text        NOT NULL DEFAULT '',
        -- Slash-separated folder; folders exist only as prefixes of this column.
        folder_path            text        NOT NULL DEFAULT '',
        tags                   text[]      NOT NULL DEFAULT '{}',
        max_tokens             integer     NULL,
        -- The spec's schedule, scope and run settings (the spec fields without a column of their own).
        refresh_settings         jsonb       NOT NULL DEFAULT '{}',
        overrides              jsonb       NULL,
        content_md             text        NOT NULL DEFAULT '',
        structured_output_json jsonb       NULL,
        version                bigint      NOT NULL DEFAULT 0,
        last_refreshed_at      timestamptz NULL,
        -- The bank change_seq the current content reflects; staleness compares it with newer writes.
        last_seq_seen          bigint      NOT NULL DEFAULT 0,
        last_refresh_failed_at timestamptz NULL,
        auto_refresh_paused    boolean     NOT NULL DEFAULT false,
        -- Cumulative ids of the memories the content was built from.
        source_ids             uuid[]      NOT NULL DEFAULT '{}',
        last_trace             jsonb       NULL,
        embedding              vector(768) NULL,
        -- Copy of refresh_settings.schedule.cron, so the cron tick can use an index.
        refresh_cron           text        NULL,
        created_at             timestamptz NOT NULL DEFAULT now(),
        updated_at             timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, bank_id, id),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
    )
    """,
    "CREATE INDEX mental_models_embedding_hnsw ON mental_models USING hnsw (embedding vector_cosine_ops)",
    "CREATE INDEX mental_models_tags_gin ON mental_models USING gin (tags)",
    "CREATE INDEX mental_models_refresh_cron_idx ON mental_models (refresh_cron) WHERE refresh_cron IS NOT NULL",
    # Previous versions and refresh failures of each model.
    """
    CREATE TABLE mental_model_history (
        id              bigserial   PRIMARY KEY,
        tenant_id       text        NOT NULL,
        bank_id         text        NOT NULL,
        mental_model_id text        NOT NULL,
        kind            text        NOT NULL CHECK (kind IN ('version', 'refresh_failed')),
        version         bigint      NULL,
        previous_content text       NOT NULL DEFAULT '',
        outcome         text        NOT NULL DEFAULT '',
        failure_reason  text        NOT NULL DEFAULT '',
        error_message   text        NOT NULL DEFAULT '',
        changed_at      timestamptz NOT NULL DEFAULT now(),
        FOREIGN KEY (tenant_id, bank_id, mental_model_id)
            REFERENCES mental_models (tenant_id, bank_id, id) ON DELETE CASCADE
    )
    """,
    """
    CREATE INDEX mental_model_history_model_idx
        ON mental_model_history (tenant_id, bank_id, mental_model_id, kind, changed_at)
    """,
]

DIRECTIVES = [
    # Per-bank rules injected into reflect, selected by tags and the active flag.
    """
    CREATE TABLE directives (
        tenant_id  text        NOT NULL,
        bank_id    text        NOT NULL,
        id         text        NOT NULL,
        name       text        NOT NULL DEFAULT '',
        rules      text[]      NOT NULL DEFAULT '{}',
        tags       text[]      NOT NULL DEFAULT '{}',
        active     boolean     NOT NULL DEFAULT true,
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (tenant_id, bank_id, id),
        FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
    )
    """,
    # ListDirectives pages a bank's directives oldest first by (created_at, id).
    'CREATE INDEX directives_bank_created_idx ON directives (tenant_id, bank_id, created_at, id COLLATE "C")',
    # A directive's name is unique within its bank: CreateDirective and UpdateDirective refuse a taken one.
    "CREATE UNIQUE INDEX directives_bank_name_key ON directives (tenant_id, bank_id, name)",
]

JOBS = [
    # Every background job is an operations row, claimed with FOR UPDATE SKIP LOCKED. No foreign key
    # to banks: bank delete's final transaction removes the bank's operations and events itself.
    """
    CREATE TABLE operations (
        id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id    text        NOT NULL,
        bank_id      text        NOT NULL,
        kind         text        NOT NULL CHECK (kind IN (
                         'retain_batch', 'retain_document', 'consolidate', 'rederive_memory',
                         'retag_document', 'refresh_mental_model', 'graph_maintenance', 'delete_bank')),
        status       text        NOT NULL DEFAULT 'pending' CHECK (status IN (
                         'pending', 'processing', 'completed', 'failed', 'cancelled')),
        -- Jobs sharing a serial key run one at a time per bank.
        serial_key   text        NULL,
        payload      jsonb       NOT NULL DEFAULT '{}',
        overrides    jsonb       NULL,
        -- A deduplicating enqueue may join this row: true only for rows inserted under a dedupe
        -- policy without overrides (the Go side owns the policy); Cancel clears it.
        joinable     boolean     NOT NULL DEFAULT false,
        parent_id    uuid        NULL,
        attempts     integer     NOT NULL DEFAULT 0,
        run_at       timestamptz NOT NULL DEFAULT now(),
        worker_id    text        NULL,
        heartbeat_at timestamptz NULL,
        progress     jsonb       NULL,
        usage        jsonb       NOT NULL DEFAULT '[]',
        result       jsonb       NULL,
        error        text        NULL,
        created_at   timestamptz NOT NULL DEFAULT now(),
        updated_at   timestamptz NOT NULL DEFAULT now(),
        finished_at  timestamptz NULL,
        -- Cancelling a processing row sets this and leaves it 'processing', so it keeps holding its
        -- serial key; the handler's terminal Complete / Fail (or the stale reclaim) then cancels it.
        -- A pending row is cancelled directly.
        cancel_requested_at timestamptz NULL,
        CONSTRAINT operations_finished_at_terminal_only
            CHECK (finished_at IS NULL OR status IN ('completed', 'failed', 'cancelled')),
        CONSTRAINT operations_cancel_requested_at_status
            CHECK (cancel_requested_at IS NULL OR status IN ('processing', 'cancelled'))
    )
    """,
    # The claim scans pending rows by run_at.
    "CREATE INDEX operations_pending_run_at_idx ON operations (run_at) WHERE status = 'pending'",
    # At most one processing row per (bank, serial key): this backs the one-at-a-time claim.
    """
    CREATE UNIQUE INDEX operations_processing_serial_key_uniq ON operations (tenant_id, bank_id, serial_key)
        WHERE status = 'processing'
    """,
    # Active rows by serial key: the claim's NOT EXISTS and the dedupe lookup.
    """
    CREATE INDEX operations_active_serial_key_idx ON operations (tenant_id, bank_id, serial_key, kind)
        WHERE status IN ('pending', 'processing')
    """,
    # Stale-heartbeat reclaim.
    "CREATE INDEX operations_processing_heartbeat_idx ON operations (heartbeat_at) WHERE status = 'processing'",
    "CREATE INDEX operations_parent_idx ON operations (parent_id) WHERE parent_id IS NOT NULL",
    "CREATE INDEX operations_bank_created_idx ON operations (tenant_id, bank_id, created_at)",
    # Retention pruning of finished rows.
    """
    CREATE INDEX operations_terminal_finished_idx ON operations (finished_at)
        WHERE status IN ('completed', 'failed', 'cancelled')
    """,
    # Events written in one transaction share the bank change_seq, so seq is not unique; id orders
    # events within a seq.
    """
    CREATE TABLE lifecycle_events (
        id           bigserial   PRIMARY KEY,
        tenant_id    text        NOT NULL,
        bank_id      text        NOT NULL,
        seq          bigint      NOT NULL,
        type         text        NOT NULL,
        at           timestamptz NOT NULL DEFAULT now(),
        operation_id uuid        NULL,
        subject_id   text        NOT NULL DEFAULT ''
    )
    """,
    "CREATE INDEX lifecycle_events_seq_idx ON lifecycle_events (tenant_id, bank_id, seq)",
    "CREATE INDEX lifecycle_events_at_idx ON lifecycle_events (at)",
]

# Every table, in creation order. downgrade() drops them in reverse; their indexes go with them.
TABLES = [
    "tenants",
    "banks",
    "documents",
    "chunks",
    "memory_units",
    "entities",
    "unit_entities",
    "entity_cooccurrences",
    "memory_links",
    "graph_dirty",
    "entity_maintenance",
    "observation_evidence",
    "observation_scopes",
    "observation_versions",
    "memory_units_archive",
    "memory_audit",
    "mental_models",
    "mental_model_history",
    "directives",
    "operations",
    "lifecycle_events",
]


def upgrade() -> None:
    """Create the extensions, then every area's tables and indexes."""
    for area in (
        EXTENSIONS,
        TENANTS_AND_BANKS,
        DOCUMENTS,
        MEMORY_UNITS,
        GRAPH,
        OBSERVATIONS,
        CURATION,
        MENTAL_MODELS,
        DIRECTIVES,
        JOBS,
    ):
        for ddl in area:
            op.execute(ddl)


def downgrade() -> None:
    """Drop every table in reverse order, then the extensions and the paradedb schema pg_search creates."""
    for table in reversed(TABLES):
        op.execute(f"DROP TABLE {table}")
    op.execute("DROP EXTENSION IF EXISTS pg_search")
    op.execute("DROP SCHEMA IF EXISTS paradedb")
    op.execute("DROP EXTENSION IF EXISTS pg_trgm")
    op.execute("DROP EXTENSION IF EXISTS vector")
