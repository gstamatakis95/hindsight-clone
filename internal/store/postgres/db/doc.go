// Package db is the sqlc-generated query layer of the Postgres adapter. sqlc (`make gen`, which runs `sqlc generate` in
// internal/store/postgres with internal/store/postgres/sqlc.yaml) reads the schema from schema/schema.sql, the
// pg_dump of the Alembic-migrated database, and the named queries from internal/store/postgres/queries/*.sql, and
// writes every other file of this directory.
//
// # Rules
//
// Never edit the generated files by hand: change the SQL (or, through a migration, the schema) and rerun `make gen`,
// which must leave no diff. This doc.go is the only hand-written file; sqlc does not generate or delete it. Only
// internal/store/postgres imports this package; engines, the server and the worker reach the database through
// internal/store.
//
// # Contents
//
// [New] binds the generated [Queries] to a [DBTX] (a pgxpool.Pool or a pgx.Tx); one method per named query, each
// carrying the query's leading SQL comment, plus a Params struct for multi-argument queries and a Row struct for
// multi-column results. [Querier] is the generated interface over all of them (emit_interface); the adapter calls
// *Queries directly. models.go holds one struct per table ([MemoryUnit], [Operation], ...).
//
//	q := db.New(pgTx)
//	seq, err := q.BumpBankSeq(ctx, db.BumpBankSeqParams{TenantID: tenant, ID: bank})
//
// Type mapping (sqlc.yaml overrides): uuid is github.com/google/uuid.UUID (uuid.NullUUID when nullable), vector is
// pgvector.Vector (*pgvector.Vector when nullable), timestamptz is time.Time (*time.Time when nullable), jsonb is
// json.RawMessage, and nullable text, int4 and int8 are *string, *int32 and *int64. Empty results are empty slices,
// never nil.
//
// Queries the adapter must build at run time (the recall arms' variable tag predicates and the NewestSeqInScope read)
// are not here: internal/store/postgres renders them with bind parameters and runs them on the same DBTX. The queue's
// claim (ClaimSelect) is the SPEC "Jobs" claim SQL verbatim except for a ::text[] cast on $1.
//
// # Testing
//
// The generated code has no tests of its own; it is exercised through internal/store/postgres's tests against a
// migrated ParadeDB database (internal/store/postgres/pgtest). The generated
// types follow the schema conventions: text ids for client-supplied ids, uuid for server ids, text enums with CHECK
// constraints and jsonb for config, overrides, progress and usage.
package db
