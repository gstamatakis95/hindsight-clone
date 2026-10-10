"""Retrieval, claim and supporting indexes for the tables of 0002-0007.

Retrieval: HNSW (vector_cosine_ops) on memory_units and mental_models embeddings, a
pg_search BM25 index on memory_units, a trigram index on entity names, tag GIN indexes.
Jobs: the partial pending-claim index, the unique (tenant, bank, serial_key) WHERE
processing index that backs the one-at-a-time-per-key claim, and the active index over
every row that holds or queues a serial key (pending, processing, and cancelled rows whose
handler is still running, i.e. finished_at IS NULL). The claim's NOT EXISTS and the dedupe
lookup (equality on all four columns, plus ``joinable``) both use it. The reclaim index
covers cancelled-but-running rows too, so ReclaimStale can finish them. Constraint-like
unique indexes stay with their tables (e.g. entities_label_uniq in 0003).

The unconsolidated index excludes observations (``fact_type <> 'observation'``): only raw
facts are consolidated; observations are its output and are never consolidated themselves.

pg_search 0.26 made the ``key_field`` option a deprecated no-op, so it is omitted; the uuid
``id`` is listed first, following ParadeDB's convention for the key column. Filter columns use the ``pdb.literal``
tokenizer so tenant / bank / fact type filters are exact-match and pushed into the scan.

Revision ID: 0008
Revises: 0007
Create Date: 2026-10-10
"""

from alembic import op

revision = "0008"
down_revision = "0007"
branch_labels = None
depends_on = None

# (index name, CREATE INDEX statement). downgrade() drops them in reverse order.
INDEXES: list[tuple[str, str]] = [
    # memory_units: retrieval arms
    (
        "memory_units_embedding_hnsw",
        "CREATE INDEX memory_units_embedding_hnsw ON memory_units USING hnsw (embedding vector_cosine_ops)",
    ),
    (
        "memory_units_bm25",
        """CREATE INDEX memory_units_bm25 ON memory_units USING bm25 (
               id, text, context,
               (tenant_id::pdb.literal), (bank_id::pdb.literal), (fact_type::pdb.literal))""",
    ),
    ("memory_units_tags_gin", "CREATE INDEX memory_units_tags_gin ON memory_units USING gin (tags)"),
    (
        "memory_units_occurred_start_idx",
        "CREATE INDEX memory_units_occurred_start_idx ON memory_units (tenant_id, bank_id, occurred_start)",
    ),
    # memory_units: consolidation, staleness, lists, cascades. Observations are never
    # consolidated (they are consolidation's output), hence fact_type <> 'observation'.
    (
        "memory_units_unconsolidated_idx",
        """CREATE INDEX memory_units_unconsolidated_idx ON memory_units (tenant_id, bank_id, created_at)
               WHERE consolidated_at IS NULL AND consolidation_failed_at IS NULL AND fact_type <> 'observation'""",
    ),
    (
        "memory_units_observation_scope_idx",
        """CREATE INDEX memory_units_observation_scope_idx ON memory_units (tenant_id, bank_id, scope_key)
               WHERE fact_type = 'observation'""",
    ),
    ("memory_units_seq_idx", "CREATE INDEX memory_units_seq_idx ON memory_units (tenant_id, bank_id, seq)"),
    (
        "memory_units_document_idx",
        "CREATE INDEX memory_units_document_idx ON memory_units (tenant_id, bank_id, document_id)",
    ),
    (
        "memory_units_created_at_idx",
        "CREATE INDEX memory_units_created_at_idx ON memory_units (tenant_id, bank_id, created_at)",
    ),
    (
        "memory_units_chunk_idx",
        "CREATE INDEX memory_units_chunk_idx ON memory_units (chunk_id) WHERE chunk_id IS NOT NULL",
    ),
    # documents
    ("documents_tags_gin", "CREATE INDEX documents_tags_gin ON documents USING gin (tags)"),
    # graph
    (
        "entities_name_trgm",
        "CREATE INDEX entities_name_trgm ON entities USING gin (lower(canonical_name) gin_trgm_ops)",
    ),
    (
        "entities_name_idx",
        "CREATE INDEX entities_name_idx ON entities (tenant_id, bank_id, lower(canonical_name))",
    ),
    ("unit_entities_entity_idx", "CREATE INDEX unit_entities_entity_idx ON unit_entities (entity_id)"),
    (
        "entity_cooccurrences_entity_b_idx",
        "CREATE INDEX entity_cooccurrences_entity_b_idx ON entity_cooccurrences (entity_b)",
    ),
    ("memory_links_to_unit_idx", "CREATE INDEX memory_links_to_unit_idx ON memory_links (to_unit_id)"),
    # observations
    ("observation_evidence_unit_idx", "CREATE INDEX observation_evidence_unit_idx ON observation_evidence (unit_id)"),
    # mental models
    (
        "mental_models_embedding_hnsw",
        "CREATE INDEX mental_models_embedding_hnsw ON mental_models USING hnsw (embedding vector_cosine_ops)",
    ),
    ("mental_models_tags_gin", "CREATE INDEX mental_models_tags_gin ON mental_models USING gin (tags)"),
    (
        "mental_models_refresh_cron_idx",
        "CREATE INDEX mental_models_refresh_cron_idx ON mental_models (refresh_cron) WHERE refresh_cron IS NOT NULL",
    ),
    (
        "mental_model_history_model_idx",
        """CREATE INDEX mental_model_history_model_idx
               ON mental_model_history (tenant_id, bank_id, mental_model_id, kind, changed_at)""",
    ),
    # operations: claim, serialization, dedupe, reclaim, listing, pruning
    (
        "operations_pending_run_at_idx",
        "CREATE INDEX operations_pending_run_at_idx ON operations (run_at) WHERE status = 'pending'",
    ),
    (
        "operations_processing_serial_key_uniq",
        """CREATE UNIQUE INDEX operations_processing_serial_key_uniq ON operations (tenant_id, bank_id, serial_key)
               WHERE status = 'processing'""",
    ),
    (
        "operations_active_serial_key_idx",
        """CREATE INDEX operations_active_serial_key_idx ON operations (tenant_id, bank_id, serial_key, kind)
               WHERE status IN ('pending', 'processing') OR (status = 'cancelled' AND finished_at IS NULL)""",
    ),
    (
        "operations_running_heartbeat_idx",
        """CREATE INDEX operations_running_heartbeat_idx ON operations (heartbeat_at)
               WHERE status = 'processing' OR (status = 'cancelled' AND finished_at IS NULL)""",
    ),
    (
        "operations_parent_idx",
        "CREATE INDEX operations_parent_idx ON operations (parent_id) WHERE parent_id IS NOT NULL",
    ),
    (
        "operations_bank_created_idx",
        "CREATE INDEX operations_bank_created_idx ON operations (tenant_id, bank_id, created_at)",
    ),
    (
        "operations_terminal_finished_idx",
        """CREATE INDEX operations_terminal_finished_idx ON operations (finished_at)
               WHERE status IN ('completed', 'failed', 'cancelled')""",
    ),
    # lifecycle events
    ("lifecycle_events_seq_idx", "CREATE INDEX lifecycle_events_seq_idx ON lifecycle_events (tenant_id, bank_id, seq)"),
    ("lifecycle_events_at_idx", "CREATE INDEX lifecycle_events_at_idx ON lifecycle_events (at)"),
]


def upgrade() -> None:
    """Create every index in INDEXES."""
    for _, ddl in INDEXES:
        op.execute(ddl)


def downgrade() -> None:
    """Drop every index in INDEXES, in reverse order."""
    for name, _ in reversed(INDEXES):
        op.execute(f"DROP INDEX {name}")
