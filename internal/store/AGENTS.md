# AGENTS.md — internal/store and the adapters

store declares the Postgres boundary; store/postgres implements it. The other boundaries follow the same rules:
internal/graph (the memory links; served by store/postgres), internal/ai (LLM, Embedder, Reranker) with its adapter
ai/gateway, and internal/blob (Store) with its adapter blob/s3.
These rules add to the root AGENTS.md.

## Contract
- The interface doc comments are the specification: ordering, dedup, unknown-id behaviour, idempotency. A change
  starts in that comment and the adapter's tests.
- Reader is split by consumer (Search, Catalog, Freshness); the tick's cross-bank scans are Maintenance. Depend on
  the narrowest one.
- Every adapter is tested against the real backend: Postgres through `postgres/pgtest` (one cloned database per
  test, so tests run in parallel), S3 on MinIO through the shared `blob/blobtest` suite. There is no in-memory store.

## No business logic
- Adapters translate; they never decide. Scoring, budgets, config merging, model choice and what to enqueue belong
  in the services and libraries.

## Errors
- Map driver and transport errors to domain errors before returning (missing → ErrNotFound, unique →
  ErrAlreadyExists, lost race → ErrConflict, connectivity/timeouts → ErrUnavailable), wrapped with %w so callers
  test with errors.Is.

## Types and wiring
- Adapters return concrete types (`*postgres.DB` implements store.DB, store.Maintenance and jobs.Claimer;
  `*s3.Store`; `*gateway.Client`). cmd/memd is the only place they are built and narrowed to their interfaces.
- Assert each interface at compile time next to the implementation: `var _ store.DB = (*DB)(nil)`.
- An adapter imports its own boundary package, internal/domain (and internal/jsonschema for the gateway), never a
  service, library, grpcapi, jobs or maintenance. `*postgres.DB` satisfies jobs.Claimer structurally; the
  compile-time assertion sits in a test file.

## Transactions
- InTx runs at READ COMMITTED and retries the whole closure on 40P01, 40001 and pruned-entity conflicts.
- Tx.Seq bumps and caches the bank's change seq; it refuses unknown (NotFound) and tombstoned (ErrBankDeleting) banks.

## Postgres
- Alembic owns the schema (migrations/; one baseline revision until the first release); Go never migrates.
  Queries are sqlc-generated from `schema/schema.sql`: edit `postgres/queries/*.sql`, then `make gen`.
- `pgtest` uses the same Postgres image (pgvector + pg_search) as `docker-compose.yml`, so tests run against the
  extensions production uses.

## Blob providers
- blob.Store is provider-neutral: Put, Get, List (prefix, paged) and DeletePrefix, keys from `blob.Key` and
  `blob.BankPrefix`. To add a provider (GCS, Azure, ...), follow the steps in `internal/blob/doc.go`: a new
  `internal/blob/<provider>` package, its tests running `blobtest.Run`, and a MEMD_BLOB_PROVIDER case in cmd/memd.

## Graph boundary
- internal/graph declares the memory graph: graph.Reader (Expand, Degree), graph.Writer (AddLinks, DeleteUnitLinks)
  and graph.Tx (both). It imports only domain, so store.Tx.Graph() can return the transaction-bound graph.Tx; the
  graph_dirty top-up queue stays in the store (UnitRepo.DirtyLinks, ClearDirtyLinks).
- store/postgres serves it over memory_links in the caller's transaction (tx.Graph()), so links commit with the units
  they join, and postgres.DB.Graph() is the pool-backed graph.Reader cmd/memd hands recall. Expand applies the
  Types, Tags and Seq hints in SQL before the limit; recall re-checks hits against the store anyway.
- Every graph adapter runs `graphtest.Run`. To adopt a graph database later, follow `internal/graph/doc.go`: an outbox
  written next to the links (including the changes that bypass tx.Graph: archive, restore, document removal, bank
  delete), a sync job, and a graph.Reader over the database wired into recall. Do not build one before it is needed.
