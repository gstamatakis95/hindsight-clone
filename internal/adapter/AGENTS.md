# AGENTS.md — internal/adapter

Adapters connect ports to the outside world: `postgres` (port.Store, port.Claimer), `s3` (port.Blob)
and `gateway` (port.LLM, port.Embedder, port.Reranker). These rules add to the root AGENTS.md.

## Contract
- Every adapter passes its contract suite in `internal/porttest`, the same suite the in-memory fake
  in `internal/fake` passes. A behaviour change starts in the suite, then both implementations.
- Implement the port's documented semantics exactly (ordering, dedup, unknown-id behaviour,
  idempotency); the port comments are the specification.

## No business logic
- Adapters translate; they never decide. Scoring, budgets, merging config, choosing models and
  deciding what to enqueue belong in engines.

## Errors
- Map every driver or transport error to a domain error class before returning: missing rows and keys
  to `domain.ErrNotFound`, unique violations to `domain.ErrAlreadyExists`, lost races to
  `domain.ErrConflict`, connectivity and timeouts to `domain.ErrUnavailable`. Wrap with `%w` so
  callers test with `errors.Is`.

## Types and wiring
- Adapters return concrete types (one Postgres `Store` implements several ports);
  `cmd/memd/wire.go`, the only composition root, narrows each to the port it is used as.
- Assert each port at compile time next to the implementation: `var _ port.Store = (*Store)(nil)`.
- Never import `internal/engine`, `internal/server` or `internal/worker`.

## Postgres
- Alembic owns the schema; Go never migrates. Queries are sqlc-generated from `schema/schema.sql`.
- Testcontainers use the same Postgres image (pgvector + pg_search) as `docker-compose.yml`, so
  tests run against the extensions production uses.
