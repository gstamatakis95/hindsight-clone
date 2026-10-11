// Package pgtest gives every test its own migrated Postgres database, cloned from a template on one shared server:
// the same ParadeDB image as docker-compose.yml ([Image], pgvector + pg_search + pg_trgm), migrated by the
// repository's own Alembic revisions. It is a regular (non-test) package so any package's tests can import it;
// production code must never import it.
//
// # Usage
//
//	func TestX(t *testing.T) {
//		t.Parallel()
//		store := pgtest.NewStore(t) // *postgres.DB (store.DB and store.Claimer) on a fresh database
//		pool := pgtest.Pool(t)      // the same database, for raw SQL setup and assertions
//		...
//	}
//
// [NewStore] and [New] create a new database on every call; [Pool] returns the newest database created for t (or
// creates one). A test that needs two databases, or a helper that must not depend on call order, keeps the [DB] from
// [New]. Subtests get their own databases, so tests and subtests may call t.Parallel. No TestMain is needed.
//
// [ID] maps a readable fixture name ("f1", "hub") to a stable UUID, since Postgres stores unit, chunk, entity and
// operation ids as uuid; [Name] maps it back for assertions.
//
// # How it works
//
//   - The server is found once per test binary: MEMD_TEST_DATABASE_URL ([DatabaseURLEnv]) or DATABASE_URL if set
//     (any server the user may create databases on, e.g. `make up`; postgresql+psycopg:// URLs are accepted), else a
//     container shared by every test binary of one `go test` invocation. The container is named after the
//     testcontainers session id (the same for all binaries a `go test` run starts) and started with Reuse, so the
//     first binary creates it and the others attach; Ryuk removes it after the last binary exits. It runs with fsync,
//     synchronous_commit and full_page_writes off, max_connections 1000, its data directory on tmpfs and the image's
//     bootstrap script hidden (Alembic, not the image, creates the schema).
//   - The template database memd_tpl_<hash> is named after a hash of migrations/versions/*.py and migrations/env.py,
//     so a schema change gets a new template and a long-lived server serves several branches. Under a Postgres
//     advisory lock (one migrator across all test processes) a missing template is created from template0, migrated
//     with `alembic -c migrations/alembic.ini upgrade head` from the repository root (found by walking up to go.mod),
//     and only then marked IS_TEMPLATE, so an interrupted migration is redone.
//   - [New] runs CREATE DATABASE memd_t_<random> TEMPLATE memd_tpl_<hash> (tens of milliseconds), opens a pool of up
//     to [MaxConns] connections and, at cleanup, closes it and drops the database WITH (FORCE). Databases of a killed
//     run stay behind on a long-lived server; drop memd_t_* databases by hand if they pile up.
//
// # Configuration
//
//   - MEMD_REQUIRE_DOCKER ([RequireDockerEnv]): without a reachable Docker daemon (and no database URL) every test
//     that asks for a database skips, so `go test ./...` still runs the rest on a machine without Docker; with this
//     variable set (CI) those tests fail instead.
//   - MEMD_ALEMBIC: the alembic binary; default .venv/bin/alembic under the repository root, built by `make tools`. A
//     missing binary fails the template creation with "run `make tools`" (it is not needed once the template exists).
//
// When the server is reachable but the container, the connection or the migration fails, every test that asks for a
// database fails.
package pgtest
