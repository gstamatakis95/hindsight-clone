"""Observation tables: observation_evidence and observation_scopes.

Observations themselves are memory_units rows with fact_type 'observation'. Evidence links
an observation to each source unit it cites (unique per pair); observation_scopes lists the
scopes (canonical tag sets) a bank has observations in, for ListScopes.

Both observation_evidence foreign keys are ON DELETE CASCADE on purpose: an evidence row is
meaningless once either its observation or its source unit is gone (deleted, or moved to
the archive by invalidate), so it goes with whichever is removed first.

Revision ID: 0004
Revises: 0003
Create Date: 2026-10-10
"""

from alembic import op

revision = "0004"
down_revision = "0003"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create observation_evidence and observation_scopes."""
    op.execute("""
        CREATE TABLE observation_evidence (
            tenant_id      text        NOT NULL,
            bank_id        text        NOT NULL,
            observation_id uuid        NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
            unit_id        uuid        NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
            quote          text        NOT NULL DEFAULT '',
            created_at     timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (observation_id, unit_id)
        )
    """)
    op.execute("""
        CREATE TABLE observation_scopes (
            tenant_id  text        NOT NULL,
            bank_id    text        NOT NULL,
            scope_key  text        NOT NULL,
            tags       text[]      NOT NULL DEFAULT '{}',
            created_at timestamptz NOT NULL DEFAULT now(),
            updated_at timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, scope_key),
            FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
        )
    """)


def downgrade() -> None:
    """Drop the observation tables."""
    op.execute("DROP TABLE observation_scopes")
    op.execute("DROP TABLE observation_evidence")
