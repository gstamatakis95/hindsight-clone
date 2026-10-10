"""Operations (the job queue) and lifecycle_events.

Every background job is an operations row, claimed with FOR UPDATE SKIP LOCKED. Neither
table has a foreign key to banks: delete_bank's final transaction deletes the bank's
remaining operations and lifecycle_events explicitly, together with the bank row.

``joinable`` marks rows a deduplicating enqueue may join: true only for rows inserted under
a dedupe policy without overrides (refresh_mental_model rows are always joinable); Go owns
that policy; Cancel clears it. ``finished_at`` is set only on terminal rows.

Cancelling a processing row sets ``cancel_requested_at`` and leaves the status 'processing',
so the row keeps holding its serial key; the handler's terminal Complete / Fail (or
ReclaimStale) then moves it to 'cancelled'. A pending row is cancelled directly.
``cancel_requested_at`` is only set on processing or cancelled rows.

Several events written in one transaction share the bank change_seq, so
lifecycle_events.seq is not unique; id orders events within a seq.

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
            cancel_requested_at timestamptz NULL,
            CONSTRAINT operations_finished_at_terminal_only
                CHECK (finished_at IS NULL OR status IN ('completed', 'failed', 'cancelled')),
            CONSTRAINT operations_cancel_requested_at_status
                CHECK (cancel_requested_at IS NULL OR status IN ('processing', 'cancelled'))
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
