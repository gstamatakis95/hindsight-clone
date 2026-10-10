# WP-00 · Contracts and scaffold

Branch `wp/WP-00-contracts`. The plan is in `docs/wp/WP-00.md`. After merge, tag the merge commit
`contracts-v1`; the orchestrator does this.

## What was built

- [x] **Repo skeleton.**
  - The full tree from "File structure", with a `doc.go` package comment in every Go package.
  - `go.mod` (module `memd`, `go 1.25.0`; see decision 001).
  - `Makefile` with gen, lint, test, e2e, migrate, up and run, plus down, downgrade, migrate-check, fmt
    and tools.
  - `docker-compose.yml`: ParadeDB `paradedb/paradedb:0.26.1-pg17` (pgvector 0.8.6, pg_search 0.26.1,
    pg_trgm 1.6) and MinIO with a bucket init.
  - `.golangci.yml` verbatim from the spec, `.mockery.yaml`, `buf.yaml` (STANDARD + COMMENTS, breaking
    FILE) and `buf.gen.yaml` (protoc plugins run through `go tool`).
  - `tools/ifacedoc`, the stdlib check for interface-method docs and doc.go, run in `make lint`.
  - `tools/mockery`, a pinned build module for mockery.
- [x] **Proto.**
  - Split into the 12 files under `api/memory/v1/`, with a leading comment on every element.
  - Comparing compiled descriptors with the spec's single file shows them identical: 8 services,
    48 rpcs, 163 messages, 636 fields, 25 enums, 125 values.
  - Generated into `gen/memory/v1` (12 `.pb.go`, 8 `_grpc.pb.go`).
- [x] **Domain and ports.**
  - `internal/domain` has every type the ports and engines reference, plus:
    - `EmbeddingDim = 768`
    - `NormalizeTags` and `MatchTags` / `MatchGroup` / `MatchFilter` with Hindsight semantics
    - the length-prefixed `ScopeKey` encoding and its decoder
    - error values
    - string enums mirroring the proto
  - `internal/port` is the spec's Ports block, plus the ids, query and hit types, `SQLPredicate` (a tree
    with reference `Eval`), `SeqRange`, `JobKind`, `DedupePolicy`, `Usage` and the chat types. It also
    defines `BankRepo` and `MentalModelRepo`, which the spec references but never defines.
- [x] **Engine interfaces.** Each Engines-block interface lives in a type-only file in its own package:
  timex, retain, recall, consolidate, reflect, mentalmodel, bank, curate, docs, maintenance, and worker
  (Checkpoint, OperationControl). Consumer-declared copies (Recaller, Reflector, Invalidator, Linker)
  are checked assignable.
- [x] **Migrations and SQL.**
  - Alembic `0001`–`0009`, raw SQL via `op.execute`, each with a reversing `downgrade()`.
    `memory_units.embedding vector(768)`; `banks.change_seq` / `deleting_at`; `seq` on units,
    observations and events.
  - `operations` has every requested column. Also created: `lifecycle_events`, `graph_dirty`,
    `entity_maintenance`, `memory_units_archive`, `parked_links`, `memory_audit` and
    `observation_versions`.
  - Indexes:
    - HNSW `vector_cosine_ops`
    - pg_search BM25 on unit text
    - trigram on entity names
    - the partial unconsolidated index
    - the partial pending-claim index `(run_at) WHERE status='pending'`
    - unique `(tenant_id, bank_id, serial_key) WHERE status='processing'`
  - `schema/schema.sql` is a deterministic dump (pg_dump runs in the container; `\restrict` lines are
    stripped).
  - `scripts/schema-{dump,check}.sh`.
  - `internal/adapter/postgres/sqlc.yaml` with pgx/v5, uuid and pgvector overrides. It includes a
    placeholder `queries/ping.sql`, because sqlc refuses an empty query dir.
- [x] **Contract test suites.** `internal/porttest` has `RunStoreSuite` (44 subtests), `RunSeqSuite`
  (8), `RunQueueSuite` (27) and `RunBlobSuite` (7), all with real assertions.
- [x] **Agent docs.**
  - Root `AGENTS.md`, from the spec block plus a frozen-contracts section.
  - `CLAUDE.md -> AGENTS.md` symlink.
  - Scoped `api/`, `internal/engine/`, `internal/adapter/` and `migrations/` AGENTS.md files.
  - `README.md`, `migrations/README.md` and `eval/README.md` skeletons.
- [x] **Mocks.** mockery v3 (matryer template, stdlib only) covers every interface in port, the engines
  and worker, under `internal/mocks/<pkg>`.

## Acceptance checks

| Accept item | Check | Result |
|---|---|---|
| `make gen && make lint` pass; `go build ./...` succeeds | `make gen` (no diff in gen/, schema/, internal/mocks/), `make lint`, `go build ./...` | pass. golangci-lint 0 issues; ifacedoc clean; buf lint clean; ruff clean |
| `buf lint` STANDARD + COMMENTS | `buf lint` | pass, 0 findings. `buf breaking` is skipped because main has no api/ yet |
| `alembic upgrade head` then `downgrade base` on compose Postgres | `make up && make downgrade && make migrate` | pass. After base, only `alembic_version` and plpgsql remain. The migrations subagent also round-tripped each revision and got byte-identical dumps; that per-revision check was not re-run at the end |
| schema.sql diff check clean | `make migrate-check` | pass, "schema/schema.sql is up to date." |
| porttest compiles; skips name the implementing WP | `go test -race -v ./internal/porttest/` | pass, 8 skip tests (see below) |
| Tag merge commit `contracts-v1` | orchestrator | pending merge |

Each porttest skip test names the WP that will implement it:

| Skip test | Implementing WP |
|---|---|
| `TestStoreSuitePostgres`, `TestSeqSuitePostgres`, `TestQueueSuitePostgres` | WP-01 |
| `TestStoreSuiteFake`, `TestSeqSuiteFake`, `TestQueueSuiteFake`, `TestBlobSuiteFake` | WP-02 |
| `TestBlobSuiteS3` | WP-03 |

Unit tests shipped with the contracts, all passing under `-race`:
- `internal/domain`: `TestScopeKeyString`, `TestScopeKeyNoCollisions`,
  `TestDecodeScopeTagsRejectsGarbage`, `TestNormalizeTags`, `TestMatchTags` and `TestMatchGroup`.
- `internal/port`: `TestSQLPredicateEval` and `TestSeqRangeContains`.
- `internal/porttest` helper self-tests: `TestNewUUID`, `TestAngleVec` and `TestTagPredicatesDiscriminate`.

## Decisions and CCRs

- `docs/decisions/001-go-version.md`: `go 1.25.0`, because grpc v1.84 requires it; protoc plugins are
  `tool` directives.
- `002-proto-file-split.md`: the file → types mapping and an import DAG. Shared leaf types move to
  `common.proto` to avoid cycles.
- `003-postgres-image.md`: ParadeDB 0.26.1-pg17.
  - Its initdb scripts are hidden, so testcontainers must do the same.
  - The MinIO image is a community build (pgsty).
- `004-schema-conventions.md`: ids, tenant columns, FKs (none from operations or the archive), NULL
  serial keys, and BM25 without `key_field` (deprecated in pg_search 0.26).
- `005-port-contract-gaps.md`: BankRepo, MentalModelRepo, SQLPredicate, SeqRange, dedupe and attempt
  semantics, and domain aliases for the port id types.
- `006-scopekey-and-tags.md`: the encoding is tags only; normalization; Hindsight tag-match semantics.
- `007-mockery-matryer.md`: the matryer template, and building mockery with current x/tools from
  `tools/mockery`.
- `008-interface-doc-check.md`: `tools/ifacedoc`.
- `009-porttest-harness.md`: factories return an empty DB; no `t.Parallel`; real-time margins; skips
  name their WP.
- `010-domain-representation.md`: string enums, pointer optionals, JSON tags, and `ExtractedFact` in
  domain.
- `011-proto-comment-semantics.md`: behaviour the proto comments state where the spec is silent.
  - Unset enums and ordering.
  - Budgets are per-arm candidate caps.
  - Unset `MemoryState` lists both tables.
- **CCR `docs/ccr/WP-00-1.md`:** approve `golang.org/x/text` for NFC tag normalization, which the Ports
  block names explicitly.

## Known gaps

- **NFC is not applied yet.** It is pending CCR WP-00-1. Tags are trimmed, lowercased, deduplicated and
  sorted only.
- **`port.Reader` is exactly the spec's six methods.** No port exists yet for the API-path and job
  reads the later WPs need:
  - document and memory listing, observation history, stats and tag counts
  - directives, scopes and pages
  - the max seq in a model's scope
  - operation get, list, cancel-one (PENDING or PROCESSING) and retry
  - Expect CCRs from WP-05, WP-06, WP-09, WP-12 and WP-13 (decision 005).
- **The engine-level types follow the spec's text** and will likely need extending inside their own
  (unfrozen) packages:
  - `recall.ParsedQuery` has no compiled tag predicate, arm floors or seq range.
  - `StructuredProjector.Project` and `Synthesizer.Delta` return no token usage.
- **Nine `//nolint:revive` stutter suppressions** (each with a reason) cover the names the spec fixes,
  such as `recall.RecallRequest` and `bank.BankDeleter`. `.golangci.yml` is kept verbatim.
- **gofumpt groups `memd/...` imports with stdlib**, because the module path has no dot. This is
  consistent repo-wide.
- **The gRPC server interfaces in `gen/` are not mocked.** WP-06 tests them over bufconn.
- **sqlc output.** The generated `internal/adapter/postgres/db` holds only the placeholder `Ping` query
  until WP-01 adds queries. `pgx/v5`, `google/uuid` and `pgvector-go` are already in `go.mod`, so
  `make gen && go build ./...` works.
