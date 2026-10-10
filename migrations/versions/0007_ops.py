"""Operations (the job queue) and lifecycle_events.

Every background job is an operations row, claimed with FOR UPDATE SKIP LOCKED. Neither
table has a foreign key to banks: the delete_bank job and the bank's event history must
outlive the bank row. Several events written in one transaction share the bank change_seq,
so lifecycle_events.seq is not unique; id orders events within a seq.

Revision ID: 0007
Revises: 0006
Create Date: 2026-10-10
"""

from alembic import op

revision = "0007"
down_revision = "0006"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create operations and lifecycle_events."""
    op.execute("""
        CREATE TABLE operations (
            id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id    text        NOT NULL,
            bank_id      text        NOT NULL,
            kind         text        NOT NULL CHECK (kind IN (
                             'retain_batch', 'retain_document', 'consolidate', 'rederive_memory',
                             'retag_document', 'refresh_mental_model', 'graph_maintenance', 'delete_bank')),
            status       text        NOT NULL DEFAULT 'pending' CHECK (status IN (
                             'pending', 'processing', 'completed', 'failed', 'cancelled')),
            serial_key   text        NULL,
            payload      jsonb       NOT NULL DEFAULT '{}',
            overrides    jsonb       NULL,
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
            finished_at  timestamptz NULL
        )
    """)
    op.execute("""
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
    """)


def downgrade() -> None:
    """Drop lifecycle_events and operations."""
    op.execute("DROP TABLE lifecycle_events")
    op.execute("DROP TABLE operations")
