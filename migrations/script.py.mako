"""${message if message.endswith(".") else message + "."}

Revision ID: ${up_revision}
Revises: ${down_revision | comma,n}
Create Date: ${create_date}
"""

from alembic import op

revision = "${up_revision}"
down_revision = ${'"%s"' % down_revision if down_revision else "None"}
branch_labels = ${repr(branch_labels)}
depends_on = ${repr(depends_on)}


def upgrade() -> None:
    """Apply this revision (raw SQL only)."""
    op.execute("""
    """)


def downgrade() -> None:
    """Fully reverse upgrade()."""
    op.execute("""
    """)
