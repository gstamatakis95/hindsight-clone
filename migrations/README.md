# memd migrations

memd's Postgres schema is owned by Alembic, written as raw SQL (`op.execute`). The Go
service never migrates; it reads the schema through sqlc.

## Setup

```sh
python3.12 -m venv .venv                # once, at the repo root (git-ignored)
.venv/bin/pip install -e 'migrations[dev]'  # alembic, psycopg 3, ruff
```

## Running locally

```sh
make up        # docker compose: Postgres (ParadeDB image) on :55432, MinIO on :59000/:59001
make migrate   # alembic upgrade head, then regenerate schema/schema.sql
```

The DSN comes from `MEMD_DSN` or `DATABASE_URL` (default
`postgresql+psycopg://memd:memd@localhost:${MEMD_PG_PORT:-55432}/memd`). Plain
`postgres://...` DSNs are accepted. Host ports are set with `MEMD_PG_PORT`, `MEMD_MINIO_PORT`
and `MEMD_MINIO_CONSOLE_PORT`.

Other commands:

```sh
.venv/bin/alembic -c migrations/alembic.ini current
.venv/bin/alembic -c migrations/alembic.ini downgrade -1
make downgrade       # downgrade to base (empty database)
make migrate-check   # fails if schema/schema.sql is stale
make ruff            # ruff check + format --check
```

## Adding a revision

Migrations are frozen after WP-00: a schema change needs an approved CCR first.

1. Create `versions/NNNN_<slug>.py` with the next number, either by hand or with
   `.venv/bin/alembic -c migrations/alembic.ini revision --rev-id NNNN -m "<what it does>"`.
   Set `revision = "NNNN"` and `down_revision` to the previous revision.
2. Write raw SQL in `upgrade()` and the exact reverse in `downgrade()`. Keep one concern per
   revision, put `tenant_id` on every new table, and follow
   `docs/decisions/004-schema-conventions.md`.
3. Run `make migrate`, then `make downgrade && make migrate`, to prove the round trip.
4. Run `make ruff` and `make migrate-check`, then commit the revision together with the
   regenerated `schema/schema.sql`.

## How sqlc consumes the schema

`scripts/schema-dump.sh` runs `pg_dump --schema-only --no-owner --no-privileges --no-comments`
inside the compose Postgres container, so client and server versions match. It excludes
`alembic_version` and strips the per-run `\restrict` / `\unrestrict` keys and the version
header, so the dump is byte-identical across runs. The result is `schema/schema.sql`, which
`internal/adapter/postgres/sqlc.yaml` uses as its `schema:` input. Run `sqlc generate` in
`internal/adapter/postgres` after the schema changes.

## The diff check

`scripts/schema-check.sh` (`make migrate-check`) creates a scratch database in the compose
Postgres, runs `alembic upgrade head` on it from empty, dumps it to a temp file and diffs that
against the committed `schema/schema.sql`; the scratch database is dropped afterwards and the
dev database is untouched. Because it starts from empty, an edit to an already-applied revision
is caught too. A non-zero exit means a revision changed without regenerating the dump (or the
dump was edited by hand). CI runs the same check against a throwaway Postgres built from the
same image.
