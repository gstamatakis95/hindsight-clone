# AGENTS.md — memd

memd is a Go gRPC memory service: retain, recall, reflect, observations, mental models.
The design documents in docs/design are the source of truth for behaviour (docs/design/README.md is the index;
docs/design/rules-to-tests.md maps each rule to its tests); this file is the source of truth for how to change code.

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
  internal/prompts: templates. internal/jsonschema: JSON Schema checks.
- internal/jobs: the job runner, jobs.Run (progress, liveness, result) and kinds.go, the catalogue of every job
  kind's payload, serial key and Enqueue helper.
- Libraries (one concrete type each, no RPCs): internal/recall (Engine: Recall, Arms, CheckArms, CompileTags,
  SearchMentalModels, ReadPages), internal/reflect (Agent: Reflect, Project), internal/timex (Normalizer),
  internal/linking (Linker, Resolver, LabelResolver). Recall arms are the one extension point inside a library:
  the recall.Arm interface, the built-in vector, text, graph and temporal arms in internal/recall/arms (one file
  each), and their contract suite in internal/recall/armtest.
- Services, one package per gRPC service area, each exporting one `Service` built by `New(Deps)`; each owns its RPC
  logic, its job handlers (`RunXJob`) and its maintenance sweeps. Services never import each other.
  - internal/memory: MemoryService (Retain, Recall, Reflect, ListRecallArms, ListMemories, GetMemory, UpdateMemory,
    InvalidateMemory, RestoreMemory); retain_document and rederive_memory; the stranded-fact and retain-batch
    sweeps.
  - internal/observation: ObservationService (Consolidate, ListObservationScopes, ListObservationHistory,
    ClearObservations, PreviewConsolidationStrategies); consolidate; the consolidation reconcile sweep.
  - internal/document: DocumentService (ListDocuments, GetDocument, RetagDocument, ReprocessDocument);
    retag_document.
  - internal/mentalmodel: MentalModelService; refresh_mental_model; the cron and failed-refresh sweeps.
  - internal/bank: BankService (create, from a template too, get, list, delete, stats, tags, export);
    delete_bank; the stalled-delete sweep.
  - internal/configsvc: ConfigService (tenant and bank config views, reset, prompt preview, directives).
  - internal/operation: OperationService (operation and event reads, cancel, retry).
- internal/maintenance: the 60 s tick (ordered steps calling the services' sweeps) and the graph_maintenance job.
- internal/grpcapi: thin gRPC handlers, one file and handler struct per proto service (holding only the services it
  calls), proto <-> domain conversion, interceptors, status mapping, health and the readiness probes. No logic.
- internal/observability: the optional Datadog traces, metrics and JSON logs, wired in cmd/memd only.
- cmd/memd: wiring only. wire.go opens the infrastructure and builds the shared libraries; one wire_<area>.go per
  service builds it, hands it to its gRPC handlers, registers its job handlers and sets its tick steps
  (wire_maintenance.go runs last). config.go reads the environment (deployment, infrastructure and secrets only),
  configfile.go the server config file (memd.example.yaml).
- migrations/: Alembic owns the schema, one baseline revision until the first release. Never write DDL in Go.
- examples/: runnable scenarios written against the generated client only (never internal/);
  examples/internal/exampleutil is their shared helper. Keep each short, and make it exit non-zero when the
  behaviour it shows does not hold: test/e2e TestExamples runs them all.
- eval/: the benchmark harness (LoCoMo, LongMemEval, golden fixtures; eval/README.md). test/e2e: the end-to-end
  suites, their harness and the gateway emulator. tools/: ifacedoc, txcheck (its own module) and the mockery pin.
- docs/design: the design documents, known limitations and the rules-to-tests map.

## Rules
- Interfaces exist only at I/O boundaries (store, graph, ai, blob, clock, jobs.Claimer, grpcapi.ReadinessProbe) and
  for in-package polymorphism (recall.Arm, reflect's tools, retain's extraction modes, the mental-model
  synthesizers). Put `var _ Iface = (*impl)(nil)` next to each implementation. Accept
  interfaces, return structs: ai.Router, ai.Tokenizer, clock.System, config.Resolver, the libraries and the
  services are concrete; take them as pointers.
- Services, libraries and infrastructure never import adapters, grpcapi, maintenance, gen, pgx, net/http or the S3
  SDK; libraries never import services; services never import each other. To affect another service, enqueue its
  job with a jobs.Enqueue* helper inside your transaction; shared pure rules go in domain. depguard enforces this.
- Enqueue jobs only through the jobs.Enqueue* helpers (jobs/kinds.go), inside the transaction of the change. The
  helpers never call tx.Seq: call tx.Seq(bank) first yourself when the transaction changes memory content.
- Inject clock.Clock; never call time.Now (or time.Since, time.After) for logic: recency, debounce, interval
  floors and budgets must be deterministic in tests. Every LLM call resolves its binding with ai.Router.
- Doc comment on every exported identifier, every method in an interface and every package (doc.go).
- McCabe complexity <= 15 and lines <= 120. Split the function; don't suppress the linter.
- No `//nolint` without a reason comment.
- Errors are domain error classes (internal/domain/errors.go), wrapped with fmt.Errorf("%w: ..."); the API sends
  their reason as google.rpc.ErrorInfo. A validation error that names a request field or config path uses
  domain.InvalidField, so the client gets a BadRequest field violation. List RPCs apply domain.ListPage and take
  a domain.TagFilter (api/AGENTS.md has the API conventions).
- Prompts live in internal/prompts/templates. Never inline prompt text in Go.
- New behaviour knobs go in BankConfig, in their phase group (retain, observations, recall, reflect, mental_models,
  models), and in the overrides message of each RPC that may change them. Operator-only settings go in a section
  of the server config file. Environment variables are only for deployment, infrastructure and secrets.
- New retrieval arms implement recall.Arm, take only the boundary they read, run armtest, and are registered in
  recallArms (cmd/memd/wire.go); clients reach them by name. Never special-case an arm in the engine.
- No per-memory delete API. Invalidate archives a unit; upsert and reprocess replace a document's units.
- Job handlers are idempotent and re-read state from Postgres; a job can run twice after a reclaim. A handler
  takes the `*jobs.Run` it runs under: report progress with run.Report (it returns domain.ErrCancelled once the
  job is cancelled or its bank deleted), check run.Alive before an irreversible step, and record results with
  run.SetResult; keep result keys stable (GetOperation shows them as Operation.debug_summary).
- Never call an LLM, embedder, reranker, recall, reflect or timex inside DB.InTx: read, call, then write in a
  short tx. tools/txcheck enforces the lexical case (a model call written inside the InTx literal).
- Any tx that changes memory content calls tx.Seq(bank) first and stamps the rows it writes with it.
  tools/txcheck flags an Enqueue before tx.Seq in the same InTx literal.
- InTx bodies must be re-runnable: InTx retries on deadlock, serialization failure and pruned-entity conflicts,
  so reset every captured output at the top of the closure and do no side effects outside the tx.
- Links are written through tx.Graph() (the graph boundary), in the transaction that writes the units.

## Commands
- make gen      # buf generate, sqlc, mockery
- make lint     # golangci-lint, ifacedoc, txcheck, buf lint, ruff
- make test     # go test -race ./... (needs Docker, or MEMD_TEST_DATABASE_URL; see internal/store/postgres/pgtest)
- make up       # docker compose: postgres, minio
- make migrate  # alembic upgrade head, then dump schema/schema.sql
- make run      # memd all
- make e2e      # compose stack + gateway emulator + memd processes + every example; run it alone
- make examples # build and run every example against a running memd (ADDR=, TENANT=)
- make migrate-check  # fail if schema/schema.sql is stale
- make bench    # run a benchmark against memd (eval/README.md)

## Before you finish
- make gen && make lint && make test pass, with no diff left in gen/, schema/ or internal/mocks/.
- Store code is tested against real Postgres: pgtest.NewStore(t) gives each test its own migrated database.
  Postgres judges due-ness and heartbeats by its own clock: move run_at or heartbeat_at with SQL rather than
  advancing the test clock. Fakes (internal/fake) exist only for the LLM, embedder, reranker and blob; mocks
  (internal/mocks, generated by mockery, never hand-written) only for the ai boundary; tests use clock.Manual.
  A job test passes jobs.Inline(job), or jobs.NewRun over a claimer when it tests fencing.
- Each _test.go sits next to the code it tests. API behaviour through the real services is tested in cmd/memd on
  the wired stack (stack_test.go: pgtest, the gateway emulator, the worker).
- A proto change comments every new element.
- A behaviour change updates its docs/design section and its row in docs/design/rules-to-tests.md. Update
  README.md and these agent files (CLAUDE.md is a symlink to AGENTS.md) where your change makes them stale.

## API changes
Until the first release tag, the API may change freely, including renames and renumbering; make lint runs
buf lint only. After the first release, buf breaking runs against that tag.
