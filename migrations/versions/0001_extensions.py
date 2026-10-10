"""Install the extensions memd needs: pgvector, pg_trgm and ParadeDB pg_search.

vector backs the 768-dim embeddings and HNSW indexes, pg_trgm the fuzzy entity-name
lookup, pg_search the BM25 keyword arm of recall.

Revision ID: 0001
Revises:
Create Date: 2026-10-10
"""

from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create the vector, pg_trgm and pg_search extensions."""
    op.execute("CREATE EXTENSION IF NOT EXISTS vector")
    op.execute("CREATE EXTENSION IF NOT EXISTS pg_trgm")
    op.execute("CREATE EXTENSION IF NOT EXISTS pg_search")


def downgrade() -> None:
    """Drop the three extensions and the paradedb schema pg_search creates for itself."""
    op.execute("DROP EXTENSION IF EXISTS pg_search")
    op.execute("DROP SCHEMA IF EXISTS paradedb")
    op.execute("DROP EXTENSION IF EXISTS pg_trgm")
    op.execute("DROP EXTENSION IF EXISTS vector")
