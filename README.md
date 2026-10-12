# memd

memd is a stateless Go gRPC service that gives AI agents long-term memory. It is a Go implementation of
[Hindsight](https://github.com/vectorize-io/hindsight)'s memory engine. Agents **retain** text; memd extracts
facts, resolves their entities and links them. Agents **recall** with several retrieval arms fused by RRF and a
cross-encoder, and **reflect** with a tool-calling agent that answers with citations. In the background, memd
**consolidates** facts into deduplicated observations per tag scope and keeps **mental models** (synthesized Markdown
pages) fresh.

All state lives in Postgres (pgvector, pg_search, pg_trgm) and object storage, so any number of api and worker
processes can run side by side. Every model call (LLMs, the embedder, the reranker) goes through one
OpenAI-compatible AI gateway, and each step can use its own model.

![memd deployment: clients, api, worker and ingest processes over Postgres, object storage and the AI gateway](docs/design/diagrams/deployment.svg)

The whole API is one file, [`api/memory/v1/memory.proto`](api/memory/v1/memory.proto): first the conventions
every service shares, then the seven services, then each service's messages. The design documents in
[docs/design](docs/design/README.md) explain how everything works.

## Features

**Retain** ([design](docs/design/retain.md))
- Retain one or many documents per call (`Retain`), synchronously by default or as background jobs (`run_async`)
  with a batch parent operation.
- Documents are upserted by id: re-retaining replaces a document's memories, and unchanged content with unchanged
  overrides is a no-op that makes no model call.
- Five extraction modes: concise, verbose, verbatim, chunks (no LLM) and custom (your own prompt).
- Chunking that keeps chat turns and JSONL lines whole.
- Time normalization of relative dates ("last spring", "two weeks ago"), with rules first and the model only as a
  fallback.
- Entity resolution with trigram matching, co-occurrence and recency; entity hints; typed entity labels that can
  also become tags.
- Temporal, semantic and causal links between memories.
- Async jobs resume from the chunks they already committed after a crash; the latest retain of a document wins.

**Recall** ([design](docs/design/recall.md))
- Ranked memories for a query, packed into a token budget (`Recall`).
- Pluggable retrieval arms: `vector` (HNSW cosine), `text` (BM25), `graph` (link expansion) and `temporal` (time
  window). Pick any subset per request; `ListRecallArms` lists what the server has registered.
- RRF fusion, an optional cross-encoder rerank, recency, temporal and proof boosts, score floors and token packing.
- Tag filters (flat match modes, or boolean groups with fuzzy tag resolution), fact types, explicit or parsed time
  windows, budgets.
- Prefer observations over the raw facts they cite; attach chunks, source facts and entity state; a full trace with
  per-arm scores.

**Reflect** ([design](docs/design/reflect-and-mental-models.md#reflect))
- An agentic tool loop (`Reflect`) over mental models, observations and raw facts, with citations that are checked
  against what the agent actually retrieved.
- Strict failure: a failing dependency fails the call instead of answering from partial evidence. A token budget
  per call, an evidence guard, and synthesis when the context window fills up.
- Directives (standing instructions) per bank, selected by tags, plus extra directives for one call.
- Mission and disposition (skepticism, literalism, empathy) per bank.
- Structured output that matches a JSON Schema you supply.

**Memories and curation** ([design](docs/design/curation-and-operations.md#curation))
- List memories with filters and paging (`ListMemories`), read one live or invalidated memory (`GetMemory`).
- Fix a fact without a hard delete: edit the fields an update mask names (`UpdateMemory`), invalidate it so recall
  and consolidation stop seeing it (`InvalidateMemory`), and bring it back (`RestoreMemory`). Every change is
  audited, and dependent observations and mental models follow.

**Documents** ([design](docs/design/retain.md#retag-retagdocument))
- List and read documents, optionally with their original text from object storage (`ListDocuments`,
  `GetDocument`).
- Replace a document's tags and re-consolidate under them (`RetagDocument`).
- Re-extract a document from its stored text, optionally with new overrides (`ReprocessDocument`).

**Observations** ([design](docs/design/consolidation.md))
- Automatic consolidation after retain, plus manual runs with their own scopes and overrides (`Consolidate`).
- Five scope modes (combined, shared, per tag, all combinations, custom); per-scope strategies with their own
  mission, caps and models; a dry run of draft strategies (`PreviewConsolidationStrategies`).
- Deduplication of near-identical observations; a poison fact is parked instead of blocking the bank.
- List scopes with counts (`ListObservationScopes`), page an observation's versions (`ListObservationHistory`), and
  clear observations of scopes, of one memory or of the whole bank (`ClearObservations`).

**Mental models** ([design](docs/design/reflect-and-mental-models.md#mental-models))
- Pages that answer a source query over a tag and fact-type scope, organized in folders (`CreateMentalModel`,
  `GetMentalModel`, `ListMentalModels`, `UpdateMentalModel`, `DeleteMentalModel`, `DeleteMentalModelFolder`).
- Refresh after consolidation or on a cron schedule, or on demand (`RefreshMentalModel`); agentic or single-shot,
  full or delta edits; an interval floor and debounce; automatic pause after a failure.
- A refresh that writes nothing (`PreviewMentalModelRefresh`), version and failure history
  (`ListMentalModelHistory`), and search over pages (`SearchMentalModels`).

**Banks and tenants** ([design](docs/design/curation-and-operations.md#banks))
- Multi-tenant: the tenant comes from the `x-tenant-id` header and every row is tenant-scoped.
- Create a bank with initial config or from a template (`CreateBank`), export one as a template
  (`ExportBankTemplate`), get and list banks (`GetBank`, `ListBanks`), stats and tag counts (`GetBankStats`,
  `ListTags`), and delete a bank with everything it holds, blobs included (`DeleteBank`).

**Configuration** ([design](docs/design/configuration.md))
- One config shape (`BankConfig`) at every level: server < tenant < bank < strategy < request < item.
- Tenant and bank levels with views of the own and effective config (`GetTenantConfig`, `UpdateTenantConfig`,
  `ResetTenantConfig`, `GetBankConfig`, `UpdateBankConfig`, `ResetBankConfig`).
- A model per step: `models.default` plus per-slot bindings at any level, and per-request overrides messages that
  state exactly what each call may change.
- Directives (`CreateDirective`, `UpdateDirective`, `ListDirectives`, `DeleteDirective`) and the exact prompt an
  operation would send (`PreviewPrompt`).

**Operations and events** ([design](docs/design/curation-and-operations.md#operations-and-events))
- Every background job is an operation you can read, list, cancel and retry (`GetOperation`, `ListOperations`,
  `CancelOperation`, `RetryOperation`), with progress, usage and the overrides it ran with.
- A lifecycle event feed per bank, polled by sequence number (`ListEvents`).
- Durable jobs in Postgres: no broker, at most one job per serial key, heartbeats and reclaim, retries with backoff,
  bounded runs that continue themselves, and a leaderless maintenance tick.

**Operations tooling**
- Structured errors: canonical codes with a stable `ErrorInfo` reason and `BadRequest` field violations.
- Health and readiness (`grpc.health.v1`) and gRPC server reflection.
- Optional Datadog traces, metrics and JSON logs.
- Benchmarks (LoCoMo, LongMemEval, golden fixtures) and runnable examples.

## Quick start

From a clean checkout, with Go, Docker, Python 3.12 and `grpcurl` installed:

```sh
make up        # Postgres (ParadeDB image) and MinIO via docker compose; creates the memd bucket
make migrate   # alembic upgrade head (builds the Alembic venv on first use)
```

### With the gateway emulator

No AI gateway at hand? Run the deterministic emulator the end-to-end tests use. It answers every prompt with
schema-valid output and 768-dimension embeddings, so it is for trying memd out, not for real answers.
`-write-config` writes `./memd.yaml`, binding every model slot to the emulator.

```sh
go run ./test/e2e/gatewayemu/cmd/gatewayemu -write-config memd.yaml &

export MEMD_GATEWAY_URL=http://127.0.0.1:4000/v1 \
  MEMD_S3_ENDPOINT=http://localhost:59000 MEMD_S3_BUCKET=memd MEMD_S3_ACCESS_KEY=memd \
  MEMD_S3_SECRET_KEY=memd-secret MEMD_S3_PATH_STYLE=true

make run       # memd all (api and worker in one process) on :50051; MEMD_DSN comes from the Makefile
```

Then, from another terminal:

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","items":[{"content":"Alice moved to Paris in May. Alice works as a nurse."}]}' \
  localhost:50051 memory.v1.MemoryService/Retain
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","query":"Where does Alice live?"}' \
  localhost:50051 memory.v1.MemoryService/Recall
```

### With a real AI gateway

Point memd at any OpenAI-compatible gateway (LiteLLM, a provider's compatible endpoint, ...) and bind your models
in the server config file:

```sh
export MEMD_GATEWAY_URL=https://your-gateway.example/v1 MEMD_GATEWAY_API_KEY=...
cp memd.example.yaml memd.yaml
```

```yaml
# memd.yaml
defaults:
  models:
    default: {model: gpt-4o-mini}            # every chat step that has no binding of its own
    reflect: {model: gpt-4o}                 # a stronger model for the reflect loop
    mental_model: {model: gpt-4o}            # and for single-shot mental-model refreshes
    rerank: {model: bge-reranker-v2-m3}      # optional cross-encoder; unbound = no rerank
    embed: {model: nomic-embed-text-v1.5}    # must return 768 dimensions (checked at startup)
```

Then `make run`. memd refuses to start when the extract or reflect step has no model, the embedding model doesn't
return 768 dimensions, or the file has an unknown key. In production, run `memd api` and `memd worker` as two
deployments over the same Postgres, bucket and config file.

## API overview

Every call carries the tenant in the `x-tenant-id` header. The server registers gRPC reflection, so grpcurl needs no
proto files. The examples below assume the quick-start server; `<id>` stands for an id from an earlier response.

**MemoryService**: retain, recall, reflect, and browse and curate memories.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","query":"Summarize what we know about Alice"}' \
  localhost:50051 memory.v1.MemoryService/Reflect
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","memory_id":"<id>","memory":{"text":"Alice moved to Lyon in May."},"update_mask":{"paths":["text"]}}' \
  localhost:50051 memory.v1.MemoryService/UpdateMemory
```

**DocumentService**: list, read, retag and reprocess retained documents.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","document_id":"<id>","tags":{"tags":["people"]}}' \
  localhost:50051 memory.v1.DocumentService/RetagDocument
```

**ObservationService**: run consolidation and inspect or clear observations.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1"}' localhost:50051 memory.v1.ObservationService/Consolidate
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","scopes":{"tag_sets":[{"tags":["people"]}]}}' \
  localhost:50051 memory.v1.ObservationService/ClearObservations
```

**MentalModelService**: synthesized pages that stay fresh.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","spec":{"id":"alice","name":"Alice","source_query":"Who is Alice?","schedule":{"after_consolidation":true}}}' \
  localhost:50051 memory.v1.MentalModelService/CreateMentalModel
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","mental_model_id":"alice"}' \
  localhost:50051 memory.v1.MentalModelService/GetMentalModel
```

**BankService**: bank lifecycle, stats, tags and templates.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"page_size":50}' localhost:50051 memory.v1.BankService/ListBanks
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1"}' localhost:50051 memory.v1.BankService/GetBankStats
```

**ConfigService**: tenant and bank config, directives and the prompt preview.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1"}' localhost:50051 memory.v1.ConfigService/GetBankConfig
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","directive":{"name":"tone","rules":["Answer formally."],"active":true}}' \
  localhost:50051 memory.v1.ConfigService/CreateDirective
```

**OperationService**: background operations and the event feed.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","page_size":20}' \
  localhost:50051 memory.v1.OperationService/ListOperations
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","after_seq":0,"page_size":50}' \
  localhost:50051 memory.v1.OperationService/ListEvents
```

Conventions, stated once in the proto header:

- **Banks.** A write to an unknown bank creates it with tenant defaults; a read of an unknown bank is `NOT_FOUND`.
  DeleteBank tombstones the bank at once (reads `NOT_FOUND`, writes `FAILED_PRECONDITION`) and deletes everything in
  the background; poll `GetOperation` with the returned operation until it is `NOT_FOUND`.
- **Async work.** An RPC that enqueues work returns the `Operation` itself (`operation`, `operations`,
  `batch_operation`, `mental_model_operations`). `Retain` is synchronous unless `run_async` is set.
- **Paging.** Every List RPC takes `page_size` (0 = 100, at most 1000, negative = `INVALID_ARGUMENT`) and
  `page_token`, and returns `next_page_token`. `ListEvents` pages by sequence (`after_seq` / `next_after_seq`);
  `SearchMentalModels` takes `max_results` (0 = 10, at most 100).
- **Tag filters.** Every request that filters by tag takes one `TagFilter`: flat `tags` under a `match` mode, or
  boolean `groups` (not both). List RPCs accept the flat form only.
- **Patches.** Update RPCs take a FieldMask `update_mask` (empty = the populated fields; a named field left unset
  clears it); reset RPCs take a required `reset_mask` (`"*"` = everything).
- **Errors.** Every error carries a `google.rpc.ErrorInfo` (domain `memd`) whose `reason` is stable, such as
  `BANK_DELETING` or `MODEL_UNBOUND`; `INVALID_ARGUMENT` also carries a `google.rpc.BadRequest` naming the offending
  fields.
- **Deadlines.** A call without a deadline gets the server's default (`api.rpc_deadline`, 120 s). A sync Retain whose
  extraction takes longer than its deadline never completes; use `run_async` for long documents.

## Configuration

memd has three kinds of settings ([design](docs/design/configuration.md)):

- **Environment variables** (each with a flag: `MEMD_GRPC_ADDR` = `-grpc-addr`) hold only deployment,
  infrastructure and secrets: `MEMD_DSN`, `MEMD_GRPC_ADDR`, `MEMD_GATEWAY_URL`, `MEMD_GATEWAY_API_KEY`,
  `MEMD_BLOB_PROVIDER` and the `MEMD_S3_*` settings, `MEMD_WORKER_ID`, `MEMD_WORKER_SLOTS`,
  `MEMD_WORKER_HEALTH_ADDR`, `MEMD_DB_MAX_CONNS`, `MEMD_LOG_LEVEL`, `MEMD_LOG_FORMAT`, the `MEMD_DATADOG_*` switches,
  and `MEMD_CONFIG`. Run `go run ./cmd/memd` to list them with their defaults.
- **The server config file** (`MEMD_CONFIG`, default `./memd.yaml`) holds the models and every behaviour and operator
  setting. Its `defaults:` block is the server level of the config hierarchy, written like the API's `BankConfig`;
  the `gateway`, `worker`, `retention`, `api` and `observability` sections hold operator settings.
  [memd.example.yaml](memd.example.yaml) documents every key; unknown keys fail startup.
- **Per-bank and per-request config** through the API: ConfigService sets the tenant and bank levels, and each RPC
  that does work takes its own overrides message.

One `BankConfig` shape is merged server < tenant < bank < consolidation strategy < request (< item for retain). It is
grouped by phase: `retain`, `observations`, `recall`, `reflect`, `mental_models` and `models`. Scalars: set replaces
unset; groups merge field by field; a model binding is used whole; lists replace.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","config":{"observations":{"dedup_threshold":0.95}},"update_mask":{"paths":["observations.dedup_threshold"]}}' \
  localhost:50051 memory.v1.ConfigService/UpdateBankConfig
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","reset_mask":{"paths":["observations"]}}' \
  localhost:50051 memory.v1.ConfigService/ResetBankConfig
```

Each RPC that does work takes its own overrides message, holding only what that call can change:

| Message | RPCs | Fields |
|---|---|---|
| `RetainOverrides` | Retain (request and item), ReprocessDocument | `retain`, `extract_model`, `time_parse_model` |
| `RecallOverrides` | Recall | `recall` (except `recall.arms`: use the request's `arms`), `time_parse_model`, `rerank_model` |
| `ReflectOverrides` | Reflect | `reflect`, `recall` (except `recall.arms`), `reflect_model`, `structured_output_model`, `rerank_model` |
| `ConsolidateOverrides` | Consolidate | the observations group minus `enabled` / `auto_consolidation` / `reconcile_grace_seconds`, `consolidate_model`, `dedup_check_model` |
| `RederiveOverrides` | UpdateMemory, RestoreMemory | `entity_match_threshold`, `extract_model`, `time_parse_model` |
| `RefreshOverrides` | RefreshMentalModel, MentalModelSpec | `reflect` (including the loop's tool caps), `max_delta_ratio`, `reflect_model`, `mental_model_model`, `structured_output_model` |

### Model routing

Every model-backed step has its own slot: `extract`, `time_parse`, `consolidate`, `dedup_check`, `reflect`,
`structured_output`, `mental_model`, `rerank` and `embed`. At any level, `models.default` binds every chat step the
level leaves unset and `models.<slot>` binds one step. For each step the most specific level that yields a binding
wins, its own slot or its default, and the winning binding is used whole (model, temperature, top_p, max output
tokens, reasoning effort, timeout, retries, extra body fields). `rerank` and `embed` are never filled by a default;
without a `rerank` binding recall skips the cross-encoder. `embed` is set only in the server config file.

Swap every chat step of one bank, then one step for one call:

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","config":{"models":{"default":{"model":"claude-sonnet"}}},"update_mask":{"paths":["models.default"]}}' \
  localhost:50051 memory.v1.ConfigService/UpdateBankConfig
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","query":"Where does Alice live?","overrides":{"reflect_model":{"model":"gpt-4o","temperature":0}}}' \
  localhost:50051 memory.v1.MemoryService/Reflect
```

The bank's `models.default` beats a step the server file binds by name, so the first call moves every chat step of
`b1` to `claude-sonnet`; the second changes only the reflect step of one call.

## Recall arms

Recall runs several retrieval arms concurrently and fuses their ranked lists with RRF
([design](docs/design/recall.md#arms)):

| Arm | Retrieves |
|---|---|
| `vector` | Semantic similarity: cosine between the query and memory embeddings (floor 0.3 unless `min_scores.arms.vector` says otherwise) |
| `text` | Full-text search: BM25 keyword relevance |
| `graph` | Memories linked (semantic and causal links) to the query's best vector matches, up to `recall.graph_max_hops` |
| `temporal` | Memories that happened inside the query's explicit or parsed time window (skipped without one) |

A request picks arms with `arms` (empty = the bank's `recall.arms`, else all of them); an unknown or repeated name is
`INVALID_ARGUMENT`. `ReflectRequest.arms` does the same for reflect's retrieval tools. Banks and tenants set the
default set with `recall.arms`, weight arms with `recall.arm_boosts` and give an arm a retrieval floor with
`min_scores.arms`, all keyed by arm name. `ListRecallArms` lists the registered arms, so clients discover arms added
later.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{}' localhost:50051 memory.v1.MemoryService/ListRecallArms
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","query":"Who does Alice work with?","arms":["graph","vector"],"trace":true}' \
  localhost:50051 memory.v1.MemoryService/Recall
```

To add an arm, implement `recall.Arm` in `internal/recall/arms`, register it in `cmd/memd/wire_services.go` and run the
`internal/recall/armtest` contract suite; `internal/recall/doc.go` has the steps.

## Datadog (optional)

memd can send traces, metrics, logs and (separately) profiles to Datadog through a Datadog agent. It uses Datadog's
own clients directly: dd-trace-go v2 for traces and profiles, and datadog-go v5 (DogStatsD) for metrics. There is no
OpenTelemetry. **It is off by default.** When off, no tracer or metrics client starts, no instrumentation is
installed, no agent is contacted, and logs stay in the text format on stderr.

| Setting | Default | Effect |
|---|---|---|
| `MEMD_DATADOG_ENABLED` / `-datadog-enabled=true` | `false` | Turns on tracing, memd's metrics, runtime metrics, JSON logs on stdout, and trace ids in logs. |
| `MEMD_DATADOG_METRICS` / `-datadog-metrics=false` | `true` | Set it to `false` to skip memd's own metrics. Tracing and runtime metrics stay on. Ignored unless Datadog is enabled. |
| `MEMD_DATADOG_PROFILER` / `-datadog-profiler=true` | `false` | Also starts the continuous profiler. Ignored unless Datadog is enabled. |
| `MEMD_LOG_FORMAT` / `-log-format=json` | unset | `text` or `json`. When unset, it is `json` with Datadog enabled and `text` otherwise. An explicit value always wins. |
| `observability.metrics_interval` (server config file) | `15s` | How often the Postgres pool and job queue gauges are sampled. |

When enabled, the libraries read the standard Datadog variables:

- **Trace agent:** `DD_AGENT_HOST` / `DD_TRACE_AGENT_PORT`, or `DD_TRACE_AGENT_URL`. The default is `localhost:8126`.
- **DogStatsD:** `DD_DOGSTATSD_URL` (`udp://host:port` or `unix:///path`), or `DD_AGENT_HOST` with
  `DD_DOGSTATSD_PORT`. The default is `localhost:8125`.
- **Unified service tagging:** `DD_ENV`, `DD_VERSION`, and `DD_SERVICE` (default `memd`). These tag every span,
  metric and log line.
- **Tags:** `DD_TAGS`. **Sampling:** `DD_TRACE_SAMPLE_RATE` / `DD_TRACE_SAMPLING_RULES`. **Runtime metrics:**
  `DD_RUNTIME_METRICS_ENABLED` (default on with Datadog).

An unreachable agent never fails startup or a request. Traces are dropped while the tracer keeps retrying. Metrics
are fire-and-forget UDP datagrams. If a client can't start, memd logs it and runs without that part.

### Traces

- **gRPC:** one `grpc.server` span per RPC, with resource = the full method, tagged `memd.tenant` and `memd.bank`.
- **Worker jobs:** one `memd.job` span per run, with resource = the kind, carrying the operation id, attempt and
  outcome.
- **Maintenance:** one `memd.maintenance.tick` span per tick.
- **Postgres:** one `postgres.query` span per statement, recorded only inside a request, job or tick.
- **AI gateway:** one `memd.gateway.call` span per call, tagged with model slot and name, with `http.request` spans
  under it.
- **S3:** one `S3.request` span per call.

### Metrics

Every metric name starts with `memd.`. Counts are counters. Durations are distributions in milliseconds, so Datadog
computes percentiles across hosts. Sampled values are gauges.

| Metric | Type | Tags | What |
|---|---|---|---|
| `memd.grpc.requests` | count | `method`, `code` | Unary RPCs, including ones the tenant check rejects. Health checks and reflection are excluded. |
| `memd.grpc.latency` | distribution (ms) | `method`, `code` | RPC duration. |
| `memd.job.started` | count | `kind` | Job runs started by a worker. |
| `memd.job.completed`, `.retried`, `.failed`, `.released`, `.lost` | count | `kind` | How each run ended: done, rescheduled, failed for good, released by a shutdown, or lost (cancel requested, or reclaimed). |
| `memd.job.duration` | distribution (ms) | `kind`, `outcome` | Run duration, including the final status write. |
| `memd.gateway.calls` | count | `slot`, `model`, `route` | Model calls. Each call counts once, however many retries it took. |
| `memd.gateway.latency` | distribution (ms) | `slot`, `model`, `route` | Call duration, including retries. |
| `memd.gateway.errors` | count | `slot`, `model`, `route`, `error` | Failed calls. `error` is the class: `unavailable`, `deadline_exceeded`, `resource_exhausted`, `invalid_argument`, `canceled`, ..., or `other`. |
| `memd.gateway.tokens.input`, `memd.gateway.tokens.output` | count | `slot`, `model` | Tokens the gateway reported for each successful HTTP call. |
| `memd.queue.depth` | gauge | `kind`, `status` | Pending and processing jobs. `retain_batch` parents are excluded. |
| `memd.queue.oldest_pending_age_seconds` | gauge | `kind` | How long the oldest due pending job has waited past its run time. Parked retries don't count. |
| `memd.postgres.pool.acquired`, `.idle`, `.total`, `.max` | gauge | | Postgres pool connections. |
| `memd.postgres.pool.acquires`, `.empty_acquires`, `.canceled_acquires`, `.empty_acquire_wait_ms` | count | | Acquire activity since the last sample. `empty_acquires` is acquires that had to wait for a free connection. |

Every metric also carries `service`, plus `env` and `version` when `DD_ENV` and `DD_VERSION` are set. Tags are kept
low-cardinality on purpose: a metric never carries a tenant, bank, document or operation id, query text or content;
spans carry the ids instead. The queue gauges come from worker and `all` processes only. Every worker reports the
same global numbers, so graph them with `max by {kind,status}`, not `sum`. A series that empties reads 0 instead of
disappearing. memd does not report counts of memories created or updated.

### Logs

With Datadog enabled, memd writes JSON logs to stdout, one object per line, using Datadog's reserved attributes:
`timestamp`, `status` (`debug`, `info`, `warn` or `error`), `message`, and `logger.name` for the tracer's own
messages. The agent parses these without a pipeline. Every line also carries `dd.service`, plus `dd.env` and
`dd.version` when set; lines logged during a request, job or tick add `dd.trace_id` and `dd.span_id`, which link the
line to its trace. `MEMD_LOG_FORMAT=json` gives the same JSON with Datadog off, on stderr and without the `dd.*`
attributes.

The agent collects logs from the container's output; memd doesn't ship them itself. To collect them, set
`DD_LOGS_ENABLED=true` on the agent, give it access to container logs (on Docker, mount the Docker socket), and label
the memd container so its logs are tagged as Go logs from service `memd`:

- **Docker:** the label `com.datadoghq.ad.logs: '[{"source":"go","service":"memd"}]'`.
- **Kubernetes:** the pod annotation `ad.datadoghq.com/memd.logs: '[{"source":"go","service":"memd"}]'`, where
  `memd` is the container name. Set `DD_AGENT_HOST` from the node IP (`status.hostIP`) to reach the agent
  DaemonSet; that variable covers both traces and DogStatsD.

To try it locally, run an agent next to the compose stack:

```yaml
  datadog-agent:
    image: gcr.io/datadoghq/agent:7
    environment:
      DD_API_KEY: ${DD_API_KEY}
      DD_SITE: ${DD_SITE:-datadoghq.com}
      DD_APM_ENABLED: "true"
      DD_APM_NON_LOCAL_TRAFFIC: "true"
      DD_DOGSTATSD_NON_LOCAL_TRAFFIC: "true"
      DD_LOGS_ENABLED: "true"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /proc/:/host/proc/:ro
      - /sys/fs/cgroup/:/host/sys/fs/cgroup:ro
    ports: ["8126:8126", "8125:8125/udp"]
```

```sh
MEMD_DATADOG_ENABLED=true DD_ENV=dev DD_VERSION=$(git rev-parse --short HEAD) go run ./cmd/memd all
```

Prompts, model output, memory content, request bodies, metadata and SQL parameters are never recorded in spans,
metrics or Datadog tags. Span errors carry only the error class
([design](docs/design/architecture.md#observability)).

## Benchmarks

`eval/` runs benchmarks against a running memd: LoCoMo, LongMemEval and the golden fixtures. It downloads and
checksums the datasets, writes a report per run, resumes interrupted runs and compares runs. LoCoMo is scored the way
Hindsight's published LoCoMo numbers are, so the two compare directly, and the eval can run stock Hindsight side by
side. [eval/README.md](eval/README.md) has the full flow against a real AI gateway.

```sh
make bench BENCH=golden ARGS=-stub                  # smoke test, no server, no model
make bench CONFIG=eval/configs/locomo.yaml          # LoCoMo against memd on :50051
make bench-compare A=eval-results/<run> B=eval-results/<run>
make bench-list                                     # the registered benchmarks
```

To add a benchmark, write a package that implements `benchmark.Benchmark` (name, dataset file with its sha256, a
loader that turns the dataset into conversations and questions, and the defaults its published numbers use),
register it in an `init` function and add one blank import to `eval/cmd/eval/benchmarks.go`. `eval/benchmark/doc.go`
and the "Adding a benchmark" section of eval/README.md have the details.

## Testing

```sh
make test      # go test -race ./...: store code runs on real Postgres, so this needs Docker or a database URL
make e2e       # compose stack + emulated gateway + memd api/worker processes: every scenario, crash and reclaim
make lint      # golangci-lint (with depguard), ifacedoc, txcheck, buf lint, ruff
make diagrams  # re-render docs/design/diagrams (d2 v0.9.0); make diagrams-check fails on a stale SVG
```

Every test that touches the store gets its own database, cloned from a migrated template
(`internal/store/postgres/pgtest`). One ParadeDB container serves the whole `go test ./...` run and is removed when
it ends. Only model calls and blob storage are stubbed: the gRPC API is tested through the real services on a wired
in-process stack (`cmd/memd/stack_test.go`), and `make e2e` runs the built binary and every example. Run `make e2e`
on its own: two end-to-end stacks or a parallel lint on one machine can slow it into timeouts. The rules each test
asserts are listed in [docs/design/rules-to-tests.md](docs/design/rules-to-tests.md).

Environment:

- `MEMD_TEST_DATABASE_URL` (or `DATABASE_URL`): use an existing server instead of a container, for example
  `MEMD_TEST_DATABASE_URL=postgres://memd:memd@localhost:55432/memd make test` after `make up`. The user must be
  allowed to create databases. Templates are named `memd_tpl_<migrations hash>` and test databases `memd_t_<random>`;
  each test drops its database when it ends.
- `MEMD_REQUIRE_DOCKER=1`: fail, rather than skip, the database, MinIO and Kafka tests when Docker (or, for the
  database, a URL) is not available.
- `MEMD_ALEMBIC`: the alembic binary that migrates the template (default `.venv/bin/alembic`, built by
  `make tools`).

A killed test run can leave `memd_t_*` databases behind on a long-lived server. To remove them (here on the compose
Postgres):

```sh
docker compose exec -T postgres psql -U memd -d memd -Atc \
  "SELECT format('DROP DATABASE %I WITH (FORCE);', datname) FROM pg_database WHERE datname LIKE 'memd\_t\_%'" \
  | docker compose exec -T postgres psql -U memd -d memd
```

## Modules

`go list ./...` minus the generated code (`gen/`) and mocks (`internal/mocks`). Each package's `doc.go` has the
details.

**Services** (one package per gRPC service; each exports one `Service` built by `New(Deps)`, with its RPCs, job
handlers and maintenance sweeps)

| Package | Owns |
|---|---|
| `internal/memory` | MemoryService: Retain (the ingestor and the retainer), Recall, Reflect and ListRecallArms (delegating to the libraries), memory listing and curation; the `retain_document` and `rederive_memory` jobs; the stranded-fact and retain-batch sweeps. |
| `internal/document` | DocumentService: listing and reading documents, RetagDocument and ReprocessDocument; the `retag_document` job. |
| `internal/observation` | ObservationService and consolidation: the `consolidate` job (scopes, decide, dedup, apply, stamp), clears, the strategy preview and the consolidation reconcile sweep. |
| `internal/mentalmodel` | MentalModelService: the refresh pipeline (agentic and single-shot, full and delta), staleness, scheduling, the `refresh_mental_model` job, and the cron and failed-refresh sweeps. |
| `internal/bank` | BankService: create (plain or from a template), get, list, stats, tags, export, and the two-step delete with the `delete_bank` job and its resume sweep. |
| `internal/configsvc` | ConfigService: the tenant and bank config levels as views, resets, directives and the prompt preview. |
| `internal/operation` | OperationService: operation reads, cancel and retry, and the lifecycle event feed. |

**Libraries** (one concrete main type each, no RPCs)

| Package | Owns |
|---|---|
| `internal/recall` | `Engine`: the recall pipeline (parse, arms, RRF, rerank, boosts, packing, includes), tag compilation, mental-model search and page reads; the `Arm` interface. |
| `internal/recall/arms` | The built-in arms `Vector`, `Text`, `Graph` and `Temporal`, one file each. |
| `internal/recall/armtest` | The contract suite every recall arm runs (`Run`). |
| `internal/reflect` | `Agent`: the reflect tool loop, its tools, citations and the structured-output projection (`Project`). |
| `internal/timex` | `Normalizer`: time-expression rules and the TIME_PARSE fallback for retain dates and recall windows. |
| `internal/linking` | `Linker` (temporal, semantic and causal links), `Resolver` (entity resolution) and `LabelResolver` (typed labels). |

**Boundaries and adapters**

| Package | Owns |
|---|---|
| `internal/store` | The Postgres boundary: `DB`, `Tx`, `Reader` (`Search`, `Catalog`, `Freshness`), the repositories, `Queue`, `Claimer` and `Maintenance`. |
| `internal/store/postgres` | `DB`, the Postgres adapter of every store interface and of `jobs.Claimer`, with the recall searches and the job queue. |
| `internal/store/postgres/db` | The sqlc-generated queries (`Queries`) from `postgres/queries/*.sql`. |
| `internal/store/postgres/pgtest` | Test support: one migrated database per test, cloned from a template on a shared server. |
| `internal/graph` | The memory-graph boundary: `Link`, `Query`, `Reader` (Expand, Degree), `Writer` and `Tx`; how a graph database would plug in. |
| `internal/graph/graphtest` | The contract suite every graph adapter runs. |
| `internal/ai` | The AI boundary: `LLM`, `Embedder` and `Reranker`, plus the concrete `Router` (model routing per slot) and `Tokenizer` (cl100k). |
| `internal/ai/gateway` | `Client`, the OpenAI-compatible gateway adapter: structured and tool calls, embeddings, rerank, retries, concurrency caps. |
| `internal/blob` | The object-storage boundary: `Store` (Put, Get, List, DeletePrefix) and the key layout (`Key`, `BankPrefix`). |
| `internal/blob/s3` | `Store` on S3-compatible storage (AWS S3, MinIO). |
| `internal/ingest` | The message-queue boundary: `Source` (Receive, Ack, Nack, DeadLetter) with at-least-once delivery, and the `Message` it delivers. No consumer drives a source yet. |
| `internal/ingest/ingesttest` | The contract suite every ingest source runs. |
| `internal/ingest/memq` | An in-memory `Source` for tests and the suite's self-test. |
| `internal/ingest/kafka` | `Source` on a Kafka topic through a consumer group (franz-go): per-key order, local Nack, commits past contiguously settled offsets, a dead-letter topic. Not wired into cmd/memd yet. |
| `internal/blob/blobtest` | The contract suite every blob adapter runs. |
| `internal/clock` | `Clock`, with `System` and the test clock `Manual`. |

**Infrastructure**

| Package | Owns |
|---|---|
| `internal/domain` | Plain types and shared pure rules: banks, memories, documents, `BankConfig`, operations, tags and `TagFilter`, scopes, directive selection, refresh due time, paging bounds and the error classes. |
| `internal/config` | `Resolver` (the effective config of a bank, scope or request), `Merge`, `ServerDefaults`, `Validate` and the FieldMask patch helpers. |
| `internal/jobs` | `Runner` (claim, heartbeat, retry, settle), the `Run` handle a handler works through, and `kinds.go`, the catalogue of every job kind's payload, serial key and Enqueue helper. |
| `internal/maintenance` | `Service`: the 60 s maintenance tick and the `graph_maintenance` job. |
| `internal/grpcapi` | The gRPC surface: one handler type per service, interceptors, proto <-> domain conversion, FieldMask patches, error mapping with details, health and readiness probes. |
| `internal/prompts` | Every prompt template, schema and tool list, embedded and rendered by name (`Render`, `Schema`). |
| `internal/jsonschema` | The JSON Schema subset that validates structured model output. |
| `internal/observability` | `Telemetry`: the optional Datadog traces, metrics and JSON logs. |
| `internal/fake` | Test support: scripted, deterministic LLM, embedder, reranker and blob fakes. |
| `cmd/memd` | The `memd` binary: configuration (environment, flags, the server config file) and the wiring of every package; modes api, worker and all. |

**Tools**

| Package | Owns |
|---|---|
| `tools/ifacedoc` | The lint check that every interface method has a doc comment and every package a `doc.go`. |
| `tools/txcheck` | The lint analyzer that flags a model call or an Enqueue before `tx.Seq` inside an `InTx` closure (its own module). |

**Eval**

| Package | Owns |
|---|---|
| `eval/cmd/eval` | The `eval` command: run, resume, compare and list benchmarks. |
| `eval/benchmark` | The `Benchmark` interface, the registry and the dataset download cache. |
| `eval/harness` | The dataset-neutral runner: conversations, `Target`, `Judge`, `Runner`, the run journal and reports. |
| `eval/target` | `Memd` (memd over gRPC) and `Hindsight` (stock Hindsight over HTTP) targets. |
| `eval/judge` | The eval's model calls: the LLM and substring judges and the RAG answerer. |
| `eval/locomo` | The LoCoMo benchmark. |
| `eval/longmemeval` | The LongMemEval benchmarks (full and oracle). |
| `eval/golden` | The golden retain → recall fixtures. |
| `eval/stub` | An in-process stand-in for memd's API, for smoke-testing the eval without a server. |

**End-to-end tests and examples**

| Package | Owns |
|---|---|
| `test/e2e` | The end-to-end suites: scenarios, crash and reclaim, the golden fixtures and the examples against memd processes. |
| `test/e2e/harness` | Brings up the compose stack, runs memd processes and the emulator, and waits for background work. |
| `test/e2e/gatewayemu` | The deterministic gateway emulator (chat, tools, structured output, embeddings, rerank). |
| `test/e2e/gatewayemu/cmd/gatewayemu` | The emulator as a standalone server for local development. |
| `examples/chat-assistant`, `examples/model-per-step`, `examples/multi-tenant`, `examples/async-ingest`, `examples/mental-models`, `examples/curation`, `examples/directives-structured`, `examples/recall-filters` | One runnable scenario each, written against the generated client only. |
| `examples/internal/exampleutil` | The examples' shared helper: flags, a tenant-stamping connection and polling. |

## Design and limitations

- [docs/design](docs/design/README.md): architecture, storage, retain, recall, consolidation, reflect and mental
  models, curation and operations, configuration, and the rules-to-tests map. The diagrams (D2 sources in
  `docs/design/diagrams/src`, rendered SVGs beside them; `make diagrams` needs d2 v0.9.0) are listed there.
- [docs/design/known-limitations.md](docs/design/known-limitations.md): accepted gaps, their impact and workarounds.

## Examples

[examples/](examples/README.md) holds eight runnable scenarios written against the generated Go client, each with the
same calls as grpcurl commands: a chat assistant, a model per step, several tenants, asynchronous ingestion, mental
models, curation, directives with structured output, and recall filters. They run against `memd all` with the gateway
emulator (`make examples`) and are smoke-tested by `make e2e`.

## Contributing

Read [AGENTS.md](AGENTS.md) first: layout, rules and the `make gen && make lint && make test` gate. Proto rules are
in [api/AGENTS.md](api/AGENTS.md), store and adapter rules in [internal/store/AGENTS.md](internal/store/AGENTS.md),
schema rules in [migrations/AGENTS.md](migrations/AGENTS.md).

The API may change freely until the first release, so `make lint` runs `buf lint` only; after the first release,
`buf breaking` runs against that tag. The schema works the same way: until the first release it is one Alembic
revision, `migrations/versions/0001_baseline.py`, edited in place; after it, schema changes are new revisions (see
[migrations/README.md](migrations/README.md)).
