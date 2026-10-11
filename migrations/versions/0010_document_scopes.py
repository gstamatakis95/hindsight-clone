"""Documents keep the retain item's observation_scopes (CCR WP-08-2).

Revision ID: 0010
Revises: 0009
Create Date: 2026-10-10
"""

from alembic import op

revision = "0010"
down_revision = "0009"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Add documents.observation_scopes (NULL = the item set none)."""
    op.execute("ALTER TABLE documents ADD COLUMN observation_scopes jsonb")


def downgrade() -> None:
    """Drop documents.observation_scopes."""
    op.execute("ALTER TABLE documents DROP COLUMN observation_scopes")
