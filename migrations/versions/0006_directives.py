"""Directives: per-bank rules injected into reflect, filtered by tags and the active flag.

Revision ID: 0006
Revises: 0005
Create Date: 2026-10-10
"""

from alembic import op

revision = "0006"
down_revision = "0005"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create the directives table."""
    op.execute("""
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
    """)


def downgrade() -> None:
    """Drop the directives table."""
    op.execute("DROP TABLE directives")
