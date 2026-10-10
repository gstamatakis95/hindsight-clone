"""Mental model tables: mental_model_folders, mental_models and mental_model_history.

A mental model row holds its spec (name, source_query, folder, tags, trigger, overrides),
its current content and the refresh bookkeeping (version, last_seq_seen, pause flag,
cumulative source_ids). refresh_cron is copied out of the trigger so the cron tick can use
an index. History keeps previous versions and refresh failures.

Revision ID: 0005
Revises: 0004
Create Date: 2026-10-10
"""

from alembic import op

revision = "0005"
down_revision = "0004"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create mental_model_folders, mental_models and mental_model_history."""
    op.execute("""
        CREATE TABLE mental_model_folders (
            tenant_id  text        NOT NULL,
            bank_id    text        NOT NULL,
            path       text        NOT NULL,
            created_at timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, path),
            FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
        )
    """)
    op.execute("""
        CREATE TABLE mental_models (
            tenant_id              text        NOT NULL,
            bank_id                text        NOT NULL,
            id                     text        NOT NULL,
            name                   text        NOT NULL DEFAULT '',
            source_query           text        NOT NULL DEFAULT '',
            folder_path            text        NOT NULL DEFAULT '',
            tags                   text[]      NOT NULL DEFAULT '{}',
            max_tokens             integer     NULL,
            trigger                jsonb       NOT NULL DEFAULT '{}',
            overrides              jsonb       NULL,
            content_md             text        NOT NULL DEFAULT '',
            structured_output_json jsonb       NULL,
            version                bigint      NOT NULL DEFAULT 0,
            is_stale               boolean     NOT NULL DEFAULT false,
            last_refreshed_at      timestamptz NULL,
            last_seq_seen          bigint      NOT NULL DEFAULT 0,
            last_refresh_failed_at timestamptz NULL,
            auto_refresh_paused    boolean     NOT NULL DEFAULT false,
            source_ids             uuid[]      NOT NULL DEFAULT '{}',
            last_trace             jsonb       NULL,
            embedding              vector(768) NULL,
            refresh_cron           text        NULL,
            created_at             timestamptz NOT NULL DEFAULT now(),
            updated_at             timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, id),
            FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
        )
    """)
    op.execute("""
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
    """)


def downgrade() -> None:
    """Drop the mental model tables in reverse dependency order."""
    op.execute("DROP TABLE mental_model_history")
    op.execute("DROP TABLE mental_models")
    op.execute("DROP TABLE mental_model_folders")
