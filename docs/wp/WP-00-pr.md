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
  were checked assignable once during development; no test for this is committed.
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
- [x] **Contract test suites.** `internal/porttest` has `RunStoreSuite` (65 subtests), `RunSeqSuite`
  (8), `RunQueueSuite` (48) and `RunBlobSuite` (7), all with real assertions. None has run against an
  implementation yet; WP-01, WP-02 and WP-03 are the first.
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
- `012-review-round-1-rulings.md`: orchestrator rulings R1-R10 from review round 1 (dedupe and
  overrides, Upgrade merge, Reschedule, ModelBinding merge, tags vs tag_groups, event paging, worker
  fencing, cancel holding the serial key, tombstoned banks, terminal failures), and the write-side ports
  added because the spec requires them.
- **CCR `docs/ccr/WP-00-1.md`: APPROVED and applied.** `golang.org/x/text` is a direct dependency, and
  `NormalizeTag` applies NFC (`TestNormalizeTagNFC`).

## Review round 1 (all fixed on this branch)

**Rulings**
- R1-R10 are implemented in the port contracts (`internal/port/jobs.go`, `repos.go`, `store.go`), the
  proto comments, the schema and the porttest suites.
- Each one is recorded in decision 012; decisions 004, 005, 006, 008, 009 and 011 were updated to match.

**Schema** (decision 004 has the amended claim SQL)
- New column `operations.joinable`, the non-joinable marker.
- A `finished_at` CHECK on `operations`.
- The active index is now `(tenant_id, bank_id, serial_key, kind)`, and its predicate covers
  cancelled-but-unfinished rows (reverted in round 2 by R8'). A running-heartbeat index was added (also
  reverted in round 2).
- `parked_links` gained CHECKs and an index.
- Every FK's ON DELETE is explicit.

**Ports**
- Added `Tx.Directives()` / `DirectiveRepo`.
- `UnitRepo`: `Update`, `SetDocumentTags`, `AppendAudit` and `RecordObservationVersion`.
- `EntityRepo`: `PendingMaintenance` and `PruneOrphans`.
- `LinkRepo`: `Count`, `Dirty` and `ClearDirty`.
- `BankRepo.DeleteBatch`.
- `Queue`: `Get`, `List`, `Cancel`, `Retry` and `Prune`.
- `EventRepo.Prune`.
- `port.LostJobsError`.
- `SeqRange.UpTo` is now `*int64`, so a snapshot at seq 0 matches nothing.
- The `Read()` doc now says the Reader is long-lived and concurrency-safe.

**Domain**
- `TagFilter.Validate`. `MatchFilter` never drops a part, and `MatchGroup` shares `SQLPredicate.Eval`'s
  semantics.
- `ParseScopeKey` accepts canonical keys only.
- New `AuditEntry` and `OperationKind.HasFinalizer`.

**porttest**
- New coverage:
  - tenant isolation;
  - BankSeq read while a tx holds Seq, and Reader reuse across commits;
  - Tags and SeqRange on all four arms, plus a window-spanning temporal unit;
  - Restore reattaching entities and causal links;
  - dedupe against parked rows, refresh joining despite overrides, the non-joinable marker, and
    concurrent dedupe enqueuers;
  - Upgrade merge, including the concurrent case;
  - fencing and lost jobs, cancel holding the key, retry, prune;
  - OPERATION_FAILED events and the bank-delete cleanup (the refresh finalizer was removed in round 2).
- Claim must not surface `ErrConflict`, and vacuous checks were removed.

**Tooling**
- `make lint-proto` fails on an unresolvable `BREAKING_AGAINST` and accepts `BREAKING_AGAINST=contracts-v1`
  (`.git#ref=`). It skips only when the ref has no protos.
- Added a `make ruff` alias.
- ifacedoc matches skip dirs by path suffix from any root.
- `maintenance` no longer imports `retain`: `CauseMap` is now a type alias, and an assignability test
  covers it.

## Review round 2 (fixed on this branch; scope per the user: solid early iteration, no overengineering)

Each item is listed with the test that proves it, or with the file:line of the contract.

**R10' — the finalizer is dropped**
- Removed `HasFinalizer` from `internal/domain/operation.go`.
- Contract: the runner owns the attempt cap (`internal/port/jobs.go:169`). ReclaimStale past max makes
  the row FAILED with one event (`jobs.go:204`). The refresh-pause reconcile is in RunMaintenance
  (`internal/engine/mentalmodel/scheduler.go:12`, `jobs.go:197`).
- Test: `RunQueueSuite/ReclaimPastMaxFails`.

**R8' — cancel-requested flag**
- Schema: `operations.cancel_requested_at` plus a CHECK (`migrations/versions/0007_ops.py`). The spec's
  claim SQL and index predicates are restored verbatim (`0008_indexes.py`, decision 004).
- Contract: `jobs.go:149` (Cancel), `jobs.go:154` (Retry), `jobs.go:183` (Heartbeat), `jobs.go:189`
  (Alive), and `jobs.go:169` (runner obligation).
- Proto comments: `api/memory/v1/operation.proto` (CancelOperation, attempts, Retry request).
- Tests: `RunQueueSuite/CancelProcessingHoldsKey` and `RunQueueSuite/CancelProcessingReportedLost`.

**H3 — no deletes on a live bank**
- Contract: `internal/port/repos.go:169` (DeleteBatch) and `repos.go:174` (Delete). The deleter
  re-checks the tombstone before `Blob.DeletePrefix` (`internal/engine/bank/deleter.go:19`).
- Test: `RunStoreSuite/DeleteRefusesLiveBank`.

**H4 — last_seq advances past filtered events**
- Contract: `EventRepo.List` takes the types filter and returns the scanned high-water mark
  (`repos.go:132`); see also `api/memory/v1/events.proto:70`.
- Test: `RunStoreSuite/EventsTypeFilterAdvancesLastSeq`.

**Refresh `DedupeNone` is treated as Pending**
- Contract: `jobs.go:126` (Enqueue doc).
- Test: `RunQueueSuite/RefreshDedupeNoneIsPending`.

**Self-continuations use `DedupePending`**
- `jobs.go:62`, plus the docs in `internal/engine/consolidate/consolidator.go`,
  `internal/engine/maintenance/graph.go` and `internal/engine/bank/deleter.go`.

**Clearing consolidation state on edit and retag**
- Contract: `repos.go:25` (`Update`) and `repos.go:29` (`SetDocumentTags`), cross-referencing
  `DeleteCiting`.
- Test: `RunStoreSuite/UpdateAndRetagResetConsolidation`.

**Rederive support**
- Contract: `LinkRepo.DeleteForUnit` (`repos.go:102`) and `EntityRepo.Detach` (`repos.go:90`).
- Test: `RunStoreSuite/RederiveDetachAndDeleteLinks`.

**DeleteBatch covers every bank-keyed table**
- The table list is at `repos.go:169`.
- Test: `RunStoreSuite/DeleteBatchCoversAllTables`. It fills both the tombstoned bank and a same-id bank
  under another tenant; the other tenant's data survives.

**Restore**
- Skips missing entities and link partners, and stamps the tx seq (`repos.go:51`).

**Adapter obligations** (one-line doc notes, WP-01 acceptance items)
- READ COMMITTED: `internal/port/store.go:13`.
- Advisory-lock statement and order, joined row `FOR UPDATE`, bank `FOR KEY SHARE`: `jobs.go:123`.

**LOWs done because each was a one-line change**
- `ParseScopeKey` rejects any key whose re-encoding differs from the input (`internal/domain/scope.go:49`;
  `TestParseScopeKeyCanonicalOnly`).
- The `max_tokens` / budget-default comments are fixed (`api/memory/v1/memory.proto:142-144`) and
  recorded in 011.
- The `Operation.attempts` comment is fixed (`api/memory/v1/operation.proto:116`) and recorded in 011.
- Upgrade ORs only `force_full` (`jobs.go`, Upgrade doc).
- `UnitRepo.Update` covers `timeless` (`repos.go:25`).

**Docs:** decision 012 has R8' and R10' rewritten and the round-2 fixes added; decisions 004, 005, 009
and 011 are updated.

## Known gaps

**Skipped in round 2 per the user's scope**
- `Validate` does not reject `Match=EXACT` combined with groups.
- The `GraphQuery` direction is not stated.
- A non-object Upgrade payload and an empty-key dedupe are not rejected with `ErrInvalidArgument`.
- The ifacedoc `gen/` skip matches by path suffix rather than being anchored to the module root.
- `EntityRepo.Detach` does not decrement co-occurrence counts.
- Restore does not recreate missing entities; it skips them, as documented.
- `LinkRepo.Dirty` does not purge non-live rows; it only returns live units.

**Other gaps**

- **Worker fencing window (R7).** Complete and Fail carry no worker or attempt token. A worker whose job
  was reclaimed *and* re-claimed by another worker is fenced only through Heartbeat's
  `*LostJobsError`, so a stale write inside that window is possible. Closing it would change the spec's
  signatures.
- **porttest is unvalidated against real implementations.** Expected counts, orders and errors are
  first exercised by WP-01, WP-02 and WP-03. Minor points the suites leave unasserted are listed in
  decision 009.
- **`port.Reader` is exactly the spec's six methods.** No port exists yet for the API-path reads the
  later WPs need:
  - document and memory listing, observation history, stats and tag counts
  - scope listing and page reads (directive selection can use `DirectiveRepo.List` in a tx)
  - the max seq in a model's scope
  - Expect CCRs from WP-06, WP-08, WP-10, WP-12 and WP-13 (decision 005).
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
