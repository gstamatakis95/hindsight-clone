// Package postgres is the production persistence adapter. One Store implements port.Store (with its
// Tx, Reader and repos) and port.Claimer over pgx and sqlc-generated queries: Postgres with pgvector
// (HNSW cosine), pg_search (BM25) and pg_trgm serves all four recall arms, the change sequence
// (Tx.Seq) and the job queue (FOR UPDATE SKIP LOCKED claims, heartbeats, reclaim).
//
// Alembic owns the schema; this package never migrates. It returns concrete types (cmd/memd/wire.go
// narrows them to ports), maps driver errors to domain errors and must pass the internal/porttest
// suites against a real Postgres.
package postgres
