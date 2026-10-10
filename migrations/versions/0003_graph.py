"""Graph tables: entities, unit_entities, entity_cooccurrences, memory_links and the maintenance queues.

Entity names are not unique: resolution may keep two entities with the same name. Only
label entities (``label_value`` set) are unique per (tenant, bank, key, value).
graph_dirty and entity_maintenance have no foreign keys because graph maintenance
tolerates rows whose unit or entity has since vanished.

Revision ID: 0003
Revises: 0002
Create Date: 2026-10-10
"""

from alembic import op

revision = "0003"
down_revision = "0002"
branch_labels = None
depends_on = None


def upgrade() -> None:
    """Create the entity and link tables plus the graph maintenance queues."""
    op.execute("""
        CREATE TABLE entities (
            id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id      text        NOT NULL,
            bank_id        text        NOT NULL,
            canonical_name text        NOT NULL,
            kind           text        NOT NULL DEFAULT '',
            label_value    text        NULL,
            mention_count  integer     NOT NULL DEFAULT 0,
            first_seen     timestamptz NULL,
            last_seen      timestamptz NULL,
            created_at     timestamptz NOT NULL DEFAULT now(),
            updated_at     timestamptz NOT NULL DEFAULT now(),
            FOREIGN KEY (tenant_id, bank_id) REFERENCES banks (tenant_id, id) ON DELETE CASCADE
        )
    """)
    op.execute("""
        CREATE UNIQUE INDEX entities_label_uniq
            ON entities (tenant_id, bank_id, kind, label_value)
            WHERE label_value IS NOT NULL
    """)
    op.execute("""
        CREATE TABLE unit_entities (
            tenant_id text NOT NULL,
            bank_id   text NOT NULL,
            unit_id   uuid NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
            entity_id uuid NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
            PRIMARY KEY (unit_id, entity_id)
        )
    """)
    op.execute("""
        CREATE TABLE entity_cooccurrences (
            tenant_id text        NOT NULL,
            bank_id   text        NOT NULL,
            entity_a  uuid        NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
            entity_b  uuid        NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
            count     integer     NOT NULL DEFAULT 0,
            last_at   timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (entity_a, entity_b),
            CONSTRAINT entity_cooccurrences_ordered CHECK (entity_a < entity_b)
        )
    """)
    op.execute("""
        CREATE TABLE memory_links (
            tenant_id    text             NOT NULL,
            bank_id      text             NOT NULL,
            from_unit_id uuid             NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
            to_unit_id   uuid             NOT NULL REFERENCES memory_units (id) ON DELETE CASCADE,
            kind         text             NOT NULL CHECK (kind IN ('temporal', 'semantic', 'causal')),
            weight       double precision NOT NULL DEFAULT 1,
            created_at   timestamptz      NOT NULL DEFAULT now(),
            PRIMARY KEY (from_unit_id, to_unit_id, kind),
            CONSTRAINT memory_links_no_self_link CHECK (from_unit_id <> to_unit_id)
        )
    """)
    op.execute("""
        CREATE TABLE graph_dirty (
            tenant_id  text        NOT NULL,
            bank_id    text        NOT NULL,
            unit_id    uuid        NOT NULL,
            created_at timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, unit_id)
        )
    """)
    op.execute("""
        CREATE TABLE entity_maintenance (
            tenant_id  text        NOT NULL,
            bank_id    text        NOT NULL,
            entity_id  uuid        NOT NULL,
            created_at timestamptz NOT NULL DEFAULT now(),
            PRIMARY KEY (tenant_id, bank_id, entity_id)
        )
    """)


def downgrade() -> None:
    """Drop the graph tables in reverse dependency order."""
    op.execute("DROP TABLE entity_maintenance")
    op.execute("DROP TABLE graph_dirty")
    op.execute("DROP TABLE memory_links")
    op.execute("DROP TABLE entity_cooccurrences")
    op.execute("DROP TABLE unit_entities")
    op.execute("DROP TABLE entities")
