# AGENTS.md — memd

memd is a Go gRPC memory service: retain, recall, reflect, observations, mental models.
The spec is the source of truth for behaviour; this file is the source of truth for how to change code.
The spec lives at `docs/SPEC.md` (the swarm playbook at `docs/SWARM_PROMPT.md`).

## Layout
- api/memory/v1/memory.proto: the whole API, one file (rules in api/AGENTS.md). Run `make gen` after any change.
- internal/domain: plain types. internal/port: interfaces only.
- internal/engine/*: business logic, depends on port only.
- internal/adapter/*: Postgres (store + job queue), S3, gateway.
- cmd/memd/wire.go: the only place concrete types meet.
- migrations/: Alembic owns the schema, one baseline revision until the first release. Never write DDL in Go.

## Rules
- Every component is an interface; put `var _ Iface = (*impl)(nil)` next to each implementation.
- Engines never import adapters, server, worker, gen, pgx, net/http or the S3 SDK (depguard enforces this).
- Doc comment on every exported identifier, every method in an interface and every package (doc.go).
- McCabe complexity <= 15 and lines <= 120. Split the function; don't suppress the linter.
- No `//nolint` without a reason comment.
- Prompts live in internal/prompts/templates. Never inline prompt text in Go.
- New behaviour knobs go in BankConfig and must be overridable per request unless the spec says otherwise.
- No per-memory delete API. Invalidate archives a unit; upsert and reprocess replace a document's units.
- Job handlers are idempotent and re-read state from Postgres; a job can run twice after a reclaim.
- Never call an LLM, embedder or reranker inside Store.InTx. Call first, then write in a short tx.
- Any tx that changes memory content calls tx.Seq(bank) and stamps the rows it writes with it.

## Commands
- make gen      # buf generate, sqlc, mockery
- make lint     # golangci-lint, ifacedoc, buf lint, ruff
- make test     # go test -race ./... (needs Docker, or MEMD_TEST_DATABASE_URL; see internal/adapter/postgres/pgtest)
- make up       # docker compose: postgres, minio
- make migrate  # alembic upgrade head, then dump schema/schema.sql
- make run      # memd all

## Before you finish
- make gen && make lint && make test pass, with no diff left in gen/, schema/ or internal/mocks/.
- Store code is tested against real Postgres: pgtest.NewStore(t) gives each test its own migrated database.
  Fakes (internal/fake) exist only for the LLM, embedder, reranker, blob and clock.
- A proto change comments every new element.

## API changes
Until the first release tag, the API may change freely, including renames and renumbering; make lint runs
buf lint only. After the first release, buf breaking runs against that tag.
