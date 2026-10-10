"""Alembic environment for memd.

Revisions are raw SQL through ``op.execute()``; there are no SQLAlchemy models, so
``target_metadata`` is None and autogenerate is not used. The DSN comes from the
environment so the same config works locally, in CI and against testcontainers.
"""

import os
from logging.config import fileConfig

from alembic import context
from sqlalchemy import create_engine, pool

DEFAULT_PORT = os.environ.get("MEMD_PG_PORT", "55432")
DEFAULT_DSN = f"postgresql+psycopg://memd:memd@localhost:{DEFAULT_PORT}/memd"

target_metadata = None

if context.config.config_file_name is not None:
    fileConfig(context.config.config_file_name)


def database_url() -> str:
    """Return the SQLAlchemy URL from MEMD_DSN or DATABASE_URL, forcing the psycopg 3 driver.

    Plain ``postgres://`` / ``postgresql://`` DSNs (as used by pgx) are accepted and rewritten.
    """
    url = os.environ.get("MEMD_DSN") or os.environ.get("DATABASE_URL") or DEFAULT_DSN
    for prefix in ("postgresql+psycopg://", "postgresql://", "postgres://"):
        if url.startswith(prefix):
            return "postgresql+psycopg://" + url[len(prefix) :]
    return url


def run_migrations_offline() -> None:
    """Emit the SQL to stdout instead of running it (``alembic upgrade head --sql``)."""
    context.configure(
        url=database_url(),
        target_metadata=target_metadata,
        literal_binds=True,
        transaction_per_migration=True,
    )
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    """Run the migrations against the database, one transaction per revision."""
    engine = create_engine(database_url(), poolclass=pool.NullPool)
    with engine.connect() as connection:
        context.configure(
            connection=connection,
            target_metadata=target_metadata,
            transaction_per_migration=True,
        )
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
