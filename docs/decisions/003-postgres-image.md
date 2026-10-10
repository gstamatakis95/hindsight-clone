# 003 — Postgres image: `paradedb/paradedb:0.26.1-pg17` (pgvector + pg_search + pg_trgm)

## Context

memd needs three extensions in one Postgres: pgvector >= 0.8 for `vector(768)`, HNSW and
`hnsw.iterative_scan` (SPEC "Recall pipeline", "Decisions: Embedding"); ParadeDB pg_search for
the BM25 keyword arm (SPEC "Decisions: Keyword search"); and pg_trgm for fuzzy entity-name
lookup (migration 0008). SPEC "File structure" lists `docker-compose.yml # postgres (pgvector +
pg_search), minio`. pg_search is not in the stock `postgres` or `pgvector/pgvector` images.

## Decision

- **Image:** `paradedb/paradedb:0.26.1-pg17`, pinned. Never use `latest`.
- **Verified in the running container (2026-10-10):**
  - PostgreSQL 17.11 (Debian 17.11-1.pgdg13+2), pg_dump 17.11
  - vector 0.8.6
  - pg_search 0.26.1
  - pg_trgm 1.6
  - `CREATE EXTENSION vector; CREATE EXTENSION pg_trgm; CREATE EXTENSION pg_search;` all
    succeed on an empty database.
- **The image's bootstrap script is disabled.** `/docker-entrypoint-initdb.d/10_bootstrap_paradedb.sh`
  installs vector, pg_search, pg_ivm, postgis, postgis_topology, postgis_tiger_geocoder,
  fuzzystrmatch and pg_stat_statements into `template1` and `POSTGRES_DB`, and pg_cron into
  `postgres`. That would put postgis tables and the tiger/topology schemas into memd's dump,
  make 0001's `IF NOT EXISTS` a silent no-op and let `downgrade base` drop extensions it did
  not create. docker-compose.yml mounts an empty tmpfs over `/docker-entrypoint-initdb.d`, so
  the `memd` database starts with only plpgsql and migration 0001 creates exactly the three
  extensions memd uses. A side effect: the script's auto-tuning of `postgresql.conf` is skipped,
  which is fine for development.
- **Testcontainers must use the same image tag and must hide the bootstrap script the same
  way** (e.g. a tmpfs or empty directory mounted at `/docker-entrypoint-initdb.d`), then run
  `alembic upgrade head`. Otherwise its database diverges from `schema/schema.sql`. When the tag
  changes, change it in docker-compose.yml and the testcontainers setup in the same commit,
  then run `make migrate` and commit any schema.sql change.
- `shared_preload_libraries` in the image is `pg_search,pg_cron,pg_stat_statements`. pg_cron
  stays preloaded without its extension; this is harmless (possible log noise) and memd does
  not use it: cron refreshes are driven by the worker's maintenance tick.
- pg_search 0.26 makes the BM25 `key_field` option a deprecated no-op (it only warns); memd lists
  the uuid `id` first in its BM25 index. See 004.
- **Object storage:** upstream MinIO no longer publishes pullable images (`minio/minio` on
  Docker Hub and `quay.io/minio/minio` both refuse pulls). Compose uses the community build
  `pgsty/minio:RELEASE.2026-08-04T00-00-00Z` (same server, ships `mc` and `curl`). A one-shot
  `minio-init` service creates the `memd` bucket. Credentials: `memd` / `memd-secret`.

## Alternatives

- **`pgvector/pgvector:pg17` plus a custom Dockerfile that installs pg_search:** one more
  artifact to build and keep patched; ParadeDB already publishes exactly this combination.
- **`paradedb/paradedb:latest` or `:pg17`:** floating tags; a rebuild could change pg_search
  syntax or pg_dump output under us.
- **PostgreSQL 18 (`0.26.1-pg18`):** available, but 17 is the most widely deployed managed
  version today and has everything memd needs (pgvector 0.8 iterative scans work on 17).
- **Native full-text search (`tsvector` + `ts_rank_cd`, Hindsight's default):** no extension
  needed, but the SPEC decided on BM25 through pg_search.
- **Keep the bootstrap script and drop the extras in a migration:** migrations would then
  depend on the image's choices, and plain-Postgres environments would break.

## Spec lines involved

- SPEC.md 912-1017, "File structure": `docker-compose.yml` at 926, `0001_extensions.py` at 954,
  `schema/schema.sql` at 967, and 1017 ("A CI step runs `alembic upgrade head` against a throwaway
  Postgres, dumps `schema.sql`...": the throwaway Postgres is this image).
- SPEC.md 1884, "Commands": `make migrate` (upgrade head, then dump schema/schema.sql).
- SPEC.md 2019, "Recall pipeline": `hnsw.ef_search = 200`, `hnsw.iterative_scan = strict_order`
  (pgvector 0.8+); "The keyword arm is BM25 through pg_search."
- SPEC.md 2032 and 2036, "Decisions": Embedding (one `vector(768)` column, one HNSW index) and
  Keyword search (pg_search).
