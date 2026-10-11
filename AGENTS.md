# AGENTS.md — memd

memd is a Go gRPC memory service: retain, recall, reflect, observations, mental models.
The spec is the source of truth for behaviour; this file is the source of truth for how to change code.
The spec lives at `docs/SPEC.md` (the swarm playbook at `docs/SWARM_PROMPT.md`).

## Layout
- api/memory/v1/memory.proto: the whole API, one file (rules in api/AGENTS.md). Run `make gen` after any change.
- internal/domain: plain types, errors and pure rules (tag filters and predicates, scope expansion, directive
  selection, refresh due time).
- Boundaries (interfaces), each with its adapter (rules in internal/store/AGENTS.md):
  - internal/store (DB, Tx, Reader = Search + Catalog + Freshness, repos, Queue, Maintenance) —
    internal/store/postgres (sqlc in postgres/db, test harness in postgres/pgtest; also implements jobs.Claimer)
  - internal/ai (LLM, Embedder, Reranker; Router and Tokenizer are concrete) — internal/ai/gateway
  - internal/blob (Store, Key; contract suite in blob/blobtest) — internal/blob/s3, chosen by MEMD_BLOB_PROVIDER
  - internal/graph (Reader, Writer, Tx: the memory links; contract suite in graph/graphtest) — served by
    internal/store/postgres through store.Tx.Graph (writes, in the caller's transaction) and postgres.DB.Graph
    (the recall graph arm's Reader); graph/doc.go explains how a graph database would plug in
  - internal/clock (Clock; System and Manual)
- internal/config: config resolution, merge (incl. model routing), defaults, validation, FieldMask patches.
  internal/prompts: templates.
- internal/jobs: the job runner, jobs.Run (progress, liveness, result) and kinds.go, the catalogue of every job
  kind's payload, serial key and Enqueue helper. internal/maintenance: the 60 s tick and the graph_maintenance job.
- Libraries (one concrete type each, no RPCs): internal/recall (Engine: Recall, CompileTags, SearchPages,
  ReadPages), internal/reflect (Agent: Reflect, Project), internal/timex (Normalizer), internal/linking (Linker,
  Resolver, LabelResolver).
- internal/engine/*: the remaining business logic over the boundaries and libraries.
- internal/grpcapi: thin gRPC handlers, one file and handler struct per service (holding only the engines it
  calls), proto <-> domain conversion, interceptors, status mapping, health and the readiness probes.
- cmd/memd: wiring only. wire.go opens the infrastructure and builds the shared libraries; one wire_<area>.go per
  area builds its engines and gRPC handlers; handlers.go adapts the engines to job handlers. config.go reads
  the environment (deployment, infrastructure and secrets only), configfile.go the server config file
  (memd.example.yaml).
- migrations/: Alembic owns the schema, one baseline revision until the first release. Never write DDL in Go.

## Rules
- The boundaries (store, graph, ai, blob, clock) are interfaces; put `var _ Iface = (*impl)(nil)` next to each
  implementation. ai.Router, ai.Tokenizer, clock.System, config.Resolver and the libraries are concrete; take them
  as pointers.
- Engines and libraries never import adapters, grpcapi, maintenance, gen, pgx, net/http or the S3 SDK; libraries
  never import engines (depguard enforces this).
- Enqueue jobs only through the jobs.Enqueue* helpers (jobs/kinds.go), inside the transaction of the change. The
  helpers never call tx.Seq: call tx.Seq(bank) first yourself when the transaction changes memory content.
- Inject clock.Clock; never call time.Now for logic. Every LLM call resolves its binding with ai.Router.
- Doc comment on every exported identifier, every method in an interface and every package (doc.go).
- McCabe complexity <= 15 and lines <= 120. Split the function; don't suppress the linter.
- No `//nolint` without a reason comment.
- Prompts live in internal/prompts/templates. Never inline prompt text in Go.
- New behaviour knobs go in BankConfig, in their phase group (retain, observations, recall, reflect, mental_models,
  models), and in the overrides message of each RPC that may change them. Operator-only settings go in a section
  of the server config file. Environment variables are only for deployment, infrastructure and secrets.
- No per-memory delete API. Invalidate archives a unit; upsert and reprocess replace a document's units.
- Job handlers are idempotent and re-read state from Postgres; a job can run twice after a reclaim. Report
  progress and results through jobs.Run; keep result keys stable (GetOperation shows them).
- Never call an LLM, embedder or reranker inside DB.InTx. Call first, then write in a short tx.
  tools/txcheck enforces the lexical case (a model call written inside the InTx literal).
- Any tx that changes memory content calls tx.Seq(bank) and stamps the rows it writes with it.
  tools/txcheck flags an Enqueue before tx.Seq in the same InTx literal.

## Commands
- make gen      # buf generate, sqlc, mockery
- make lint     # golangci-lint, ifacedoc, txcheck, buf lint, ruff
- make test     # go test -race ./... (needs Docker, or MEMD_TEST_DATABASE_URL; see internal/store/postgres/pgtest)
- make up       # docker compose: postgres, minio
- make migrate  # alembic upgrade head, then dump schema/schema.sql
- make run      # memd all

## Before you finish
- make gen && make lint && make test pass, with no diff left in gen/, schema/ or internal/mocks/.
- Store code is tested against real Postgres: pgtest.NewStore(t) gives each test its own migrated database.
  Fakes (internal/fake) exist only for the LLM, embedder, reranker and blob; tests use clock.Manual for time.
- API behaviour through the real engines is tested in cmd/memd on the wired stack (stack_test.go: pgtest, the
  gateway emulator, the worker), not with engine mocks.
- A proto change comments every new element.

## API changes
Until the first release tag, the API may change freely, including renames and renumbering; make lint runs
buf lint only. After the first release, buf breaking runs against that tag.
