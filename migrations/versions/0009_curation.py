"""Curation tables: memory_units_archive, parked_links, memory_audit, observation_versions.

Invalidated units move to memory_units_archive with snapshots of their entities and causal
links, so recall never needs a state filter. None of these tables has a foreign key, and
nothing references them: history and audit outlive the units they describe, and
delete_bank removes their rows explicitly.

Revision ID: 0009
Revises: 0008
Create Date: 2026-10-10
"""

from alembic import op

revision = "0009"
down_revision = "0008"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create the archive, parked links, audit and observation history tables and their indexes."""
    op.execute("""
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
    """)
    op.execute("""
        CREATE TABLE parked_links (
            tenant_id        text             NOT NULL,
            bank_id          text             NOT NULL,
            archived_unit_id uuid             NOT NULL,
            other_unit_id    uuid             NOT NULL,
            kind             text             NOT NULL,
            direction        text             NOT NULL CHECK (direction IN ('out', 'in')),
            weight           double precision NOT NULL DEFAULT 1,
            created_at       timestamptz      NOT NULL DEFAULT now(),
            PRIMARY KEY (archived_unit_id, other_unit_id, kind, direction),
            CONSTRAINT parked_links_kind CHECK (kind IN ('temporal', 'semantic', 'causal')),
            CONSTRAINT parked_links_no_self_link CHECK (archived_unit_id <> other_unit_id)
        )
    """)
    op.execute("""
        CREATE TABLE memory_audit (
            id           bigserial   PRIMARY KEY,
            tenant_id    text        NOT NULL,
            bank_id      text        NOT NULL,
            unit_id      uuid        NOT NULL,
            action       text        NOT NULL CHECK (action IN ('edit', 'invalidate', 'restore', 'reprocess')),
            reason       text        NOT NULL DEFAULT '',
            before       jsonb       NULL,
            after        jsonb       NULL,
            operation_id uuid        NULL,
            at           timestamptz NOT NULL DEFAULT now()
        )
    """)
    op.execute("""
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
    """)
    op.execute("""
        CREATE INDEX memory_units_archive_bank_idx
            ON memory_units_archive (tenant_id, bank_id, archived_at)
    """)
    op.execute("""
        CREATE INDEX memory_units_archive_document_idx
            ON memory_units_archive (tenant_id, bank_id, document_id)
    """)
    op.execute("""
        CREATE INDEX memory_units_archive_operation_idx
            ON memory_units_archive (operation_id) WHERE operation_id IS NOT NULL
    """)
    op.execute("CREATE INDEX parked_links_archived_unit_idx ON parked_links (tenant_id, bank_id, archived_unit_id)")
    op.execute("CREATE INDEX parked_links_other_unit_idx ON parked_links (other_unit_id)")
    op.execute("CREATE INDEX memory_audit_unit_idx ON memory_audit (tenant_id, bank_id, unit_id, at)")
    op.execute("""
        CREATE INDEX memory_audit_operation_idx
            ON memory_audit (operation_id) WHERE operation_id IS NOT NULL
    """)
    op.execute("CREATE INDEX observation_versions_bank_idx ON observation_versions (tenant_id, bank_id)")


def downgrade() -> None:
    """Drop the curation tables; their indexes go with them."""
    op.execute("DROP TABLE observation_versions")
    op.execute("DROP TABLE memory_audit")
    op.execute("DROP TABLE parked_links")
    op.execute("DROP TABLE memory_units_archive")
