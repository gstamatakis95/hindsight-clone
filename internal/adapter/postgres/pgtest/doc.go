// Package pgtest runs a throwaway, migrated Postgres for tests: the same ParadeDB image as docker-compose.yml ([Image],
// pgvector + pg_search + pg_trgm) with the image's bootstrap script hidden (Alembic, not the image, creates the
// schema), migrated by the repository's own Alembic revisions. It is a regular (non-test) package so any package's
// tests can import it; today internal/adapter/postgres's tests do. Production code must never import it.
//
// # Usage
//
// One container serves a whole test binary. Call [Main] from TestMain and [Fresh] from each test or subtest:
//
//	func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
//
//	func TestX(t *testing.T) {
//		pool := pgtest.Fresh(t) // every table truncated
//		store := postgres.New(pool)
//		...
//	}
//
// [Shared] returns the database without truncating it; [Start] and [DB.Close] run and stop a container outside the
// Main/Fresh pair.
//
// # What it does
//
//   - Main checks that a Docker daemon is healthy, then [Start]s the container (one retry, since a start can time out
//     when many test binaries start containers at once), runs the tests and terminates it. testcontainers' Ryuk reaps
//     the container if the process dies.
//   - Start runs the image with database, user and password "memd", tmpfs over /docker-entrypoint-initdb.d (bootstrap
//     hidden, as in compose) and over the data directory (speed; the data is throwaway), waits up to 3 minutes for
//     readiness, then runs `alembic -c migrations/alembic.ini upgrade head` from the repository root (found by walking
//     up to go.mod) with MEMD_DSN pointing at the container, and opens a pool of [MaxConns] (24) connections.
//   - [DB.Reset] (called by Fresh) truncates every public table except alembic_version with RESTART IDENTITY CASCADE,
//     under lock_timeout = 10s so a leaked transaction fails the test instead of hanging it. Resets are serialized by a
//     mutex.
//
// Tests that share the container must not run in parallel with each other: every subtest expects an empty database, so
// none calls t.Parallel.
//
// # Configuration
//
//   - MEMD_REQUIRE_DOCKER ([RequireDockerEnv]): without a reachable Docker daemon every Fresh call skips its test, so
//     `make test` runs on a machine without Docker; with this variable set (CI) it fails instead.
//   - MEMD_ALEMBIC: the alembic binary; default .venv/bin/alembic under the repository root, built by `make tools`. A
//     missing binary fails the start with "run `make tools`".
//
// When Docker is up but the container or the migration fails, every test that calls Fresh fails.
package pgtest
