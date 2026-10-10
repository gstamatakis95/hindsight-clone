"""Core tables: tenants, banks, documents, chunks and memory_units.

Banks carry the change sequence (``change_seq``) and the delete tombstone (``deleting_at``).
memory_units holds raw facts (world, experience) and observations in one table, each
stamped with the bank change_seq of the transaction that wrote it.

Revision ID: 0002
Revises: 0001
Create Date: 2026-10-10
"""

from alembic import op

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create tenants, banks, documents, chunks and memory_units."""
    op.execute("""
        CREATE TABLE tenants (
            id         text        PRIMARY KEY,
            config     jsonb       NOT NULL DEFAULT '{}',
            created_at timestamptz NOT NULL DEFAULT now(),
            updated_at timestamptz NOT NULL DEFAULT now()
        )
    """)
    op.execute("""
        CREATE TABLE banks (
            tenant_id            text        NOT NULL,
            id                   text        NOT NULL,
            name                 text        NOT NULL DEFAULT '',
            config               jsonb       NOT NULL DEFAULT '{}',
            change_seq           bigint      NOT NULL DEFAULT 0,
            deleting_at          timestamptz NULL,
            last_memory_write_at timestamptz NULL,
            created_at           timestamptz NOT NULL DEFAULT now(),
            updated_at           timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, id)
        )
    """)
    op.execute("""
        CREATE TABLE documents (
            tenant_id      text        NOT NULL,
            bank_id        text        NOT NULL,
            id             text        NOT NULL,
            content_hash   text        NOT NULL DEFAULT '',
            overrides_hash text        NOT NULL DEFAULT '',
            overrides      jsonb       NULL,
            tags           text[]      NOT NULL DEFAULT '{}',
            blob_key       text        NOT NULL DEFAULT '',
            extracted_at   timestamptz NULL,
            created_at     timestamptz NOT NULL DEFAULT now(),
            updated_at     timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, id),
            FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
        )
    """)
    op.execute("""
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
    """)
    op.execute("""
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
            proof_count             integer     NOT NULL DEFAULT 0,
            scope_key               text        NULL,
            version                 bigint      NOT NULL DEFAULT 1,
            seq                     bigint      NOT NULL,
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
    """)


def downgrade() -> None:
    """Drop the core tables in reverse dependency order."""
    op.execute("DROP TABLE memory_units")
    op.execute("DROP TABLE chunks")
    op.execute("DROP TABLE documents")
    op.execute("DROP TABLE banks")
    op.execute("DROP TABLE tenants")
