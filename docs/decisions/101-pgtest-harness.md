# 101 — pgtest: the Postgres test harness

## Context

Decision 003 requires testcontainers to use the compose image with `/docker-entrypoint-initdb.d` hidden and
to migrate with Alembic; decision 009 requires a fresh, empty database per subtest. Several work packages
(WP-01 now, WP-05 / WP-14 later) need the same container setup.

## Decision

- `internal/adapter/postgres/pgtest` (a non-test package so other packages' tests can import it) starts
  one `paradedb/paradedb:0.26.1-pg17` container per test binary from `TestMain` (`pgtest.Main`), with
  tmpfs over `/docker-entrypoint-initdb.d` (bootstrap hidden, as in compose) and over the data directory
  (speed; the data is throwaway). It runs `.venv/bin/alembic -c migrations/alembic.ini upgrade head` from
  the repo root with `MEMD_DSN` pointing at the container (`MEMD_ALEMBIC` overrides the binary), then
  opens a 24-connection pool.
- `pgtest.Fresh(t)` truncates every public table except `alembic_version` (`RESTART IDENTITY CASCADE`,
  `lock_timeout = 10s` so a leaked transaction fails the test instead of hanging) and returns the pool.
- Without a reachable Docker daemon every `Fresh` call skips its test, so `make test` still runs on a
  machine without Docker; when Docker is up but the container or the migration fails, tests fail.
  A missing Alembic venv fails with "run `make tools`".
- `pgtest.Main` terminates the container when the tests finish; Ryuk reaps it if the process dies.

## Alternatives

- One container per subtest: correct but ~5 s each over hundreds of subtests.
- Applying `schema/schema.sql` with psql instead of Alembic: faster, but decision 003 asks for the
  migrations themselves, which also tests them.
- Failing (not skipping) without Docker: would make `make test` unusable on machines without Docker.

## Spec lines involved

- SPEC "Code standards": "The Postgres adapter runs it against a real Postgres (testcontainers)".
- Decisions 003 (image, hidden bootstrap) and 009 (empty database per subtest, no t.Parallel).
