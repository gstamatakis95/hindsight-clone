// Package pgtest runs a throwaway, migrated Postgres for tests: the same ParadeDB image as
// docker-compose.yml (pgvector + pg_search + pg_trgm) with the image's bootstrap script hidden (decision
// 003), migrated by the repository's Alembic (.venv/bin/alembic, built by `make tools`).
//
// One container serves a whole test binary. Call Main from TestMain and Fresh from each test or subtest:
//
//	func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
//
//	func TestX(t *testing.T) {
//		pool := pgtest.Fresh(t) // every table truncated
//		...
//	}
//
// Without a reachable Docker daemon every Fresh call skips its test, unless MEMD_REQUIRE_DOCKER is set, in
// which case it fails.
package pgtest
