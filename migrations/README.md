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

## Changing the schema

Until the first release tag there is exactly one revision, `versions/0001_baseline.py`, which
creates the whole schema. A schema change edits it in place:

1. Edit the area of `0001_baseline.py` the change belongs to (tables are grouped by area, each
   with its indexes) and, for a new table, add it to `TABLES` so `downgrade()` drops it. Put
   `tenant_id` on every new table and follow `docs/decisions/004-schema-conventions.md`.
2. Rebuild your dev database: its `alembic_version` still says `0001`, so `make migrate` alone
   would not re-run the edited baseline. Either run `make downgrade` before editing, then
   `make migrate`, or start over with `docker compose down -v && make up && make migrate`. The
   second form is the one to use after pulling someone else's baseline change.
3. Run `make downgrade && make migrate` to prove the round trip, then `make ruff`,
   `make migrate-check` and `make gen` (sqlc reads the dump). Commit the baseline together with
   `schema/schema.sql` and the regenerated sqlc code.

After the first release the baseline is frozen and every change is a new revision,
`versions/NNNN_<slug>.py` (`.venv/bin/alembic -c migrations/alembic.ini revision --rev-id NNNN -m "..."`),
with raw SQL in `upgrade()` and its exact reverse in `downgrade()`.

## How sqlc consumes the schema

`scripts/schema-dump.sh` runs `pg_dump --schema-only --no-owner --no-privileges --no-comments`
inside the compose Postgres container, so client and server versions match. It excludes
`alembic_version` and strips the per-run `\restrict` / `\unrestrict` keys and the version
header, so the dump is byte-identical across runs. The result is `schema/schema.sql`, which
`internal/store/postgres/sqlc.yaml` uses as its `schema:` input. Run `make gen` after the
schema changes.

Tests do not use the dev database: `internal/store/postgres/pgtest` migrates a template
database named after a hash of `versions/*.py` and `env.py`, so an edit to the baseline gets a
fresh template automatically.

## The diff check

`scripts/schema-check.sh` (`make migrate-check`) creates a scratch database in the compose
Postgres, runs `alembic upgrade head` on it from empty, dumps it to a temp file and diffs that
against the committed `schema/schema.sql`; the scratch database is dropped afterwards and the
dev database is untouched. Because it starts from empty, an edit to an already-applied revision
is caught too. A non-zero exit means a revision changed without regenerating the dump (or the
dump was edited by hand). CI runs the same check against a throwaway Postgres built from the
same image.
