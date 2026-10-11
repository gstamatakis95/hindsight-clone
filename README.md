# memd

memd is a stateless Go gRPC agent-memory service — a Go clone of
[Hindsight](https://github.com/vectorize-io/hindsight)'s memory engine. Agents **retain** text, **recall**
it through pluggable retrieval arms (built in: vector, BM25 text, graph, temporal; a request can pick any subset)
fused with RRF and a cross-encoder, and **reflect** over it with a tool-calling loop. Background workers consolidate facts into
observations and keep mental models (synthesized pages) fresh. All state lives in Postgres (pgvector,
pg_search, pg_trgm) and object storage; LLMs, the reranker and the embedder sit behind an
OpenAI-compatible AI gateway.

> Status: integrated and through the final review (fix-ups WP-18..21 merged). `memd api`, `memd worker`
> and `memd all` run every service; `make e2e` exercises the whole service against Postgres, MinIO and an
> emulated gateway. The behaviour is specified in `docs/SPEC.md`; `docs/rules.md` maps every behavioural rule
> to its tests; accepted gaps are listed in `docs/STATUS.md`.

## Architecture

```mermaid
flowchart TB
  C[Clients: agents, admin, eval] --> API["memd api<br/>gRPC: Retain, Recall, Reflect, admin"]
  API --> Q[("operations table<br/>job queue, SKIP LOCKED")] --> W["memd worker<br/>retain, consolidate, refresh, ..."]
  API & W --> PG[("Postgres<br/>pgvector, pg_search, pg_trgm")]
  API & W --> S3[("Object storage")]
  API & W --> GW["AI gateway (OpenAI-compatible)"]
```

The whole gRPC API is one file, [`api/memory/v1/memory.proto`](api/memory/v1/memory.proto): the shared
conventions first, then every service, then each service's messages.

## Quick start

From a clean checkout (Go, Docker, Python 3.12 and `grpcurl` installed):

```sh
make up        # Postgres (ParadeDB image) + MinIO via docker compose; creates the memd bucket
make migrate   # alembic upgrade head (builds the Alembic venv on first use)

# No AI gateway at hand? Run the deterministic emulator the e2e tests use (port 4000). It answers every
# prompt with schema-valid output and 768-dimension embeddings, so it is for trying memd out, not for
# real answers. -write-config writes ./memd.yaml, binding every model slot to the emulator.
go run ./test/e2e/gatewayemu/cmd/gatewayemu -write-config memd.yaml &

export MEMD_GATEWAY_URL=http://127.0.0.1:4000/v1 \
  MEMD_S3_ENDPOINT=http://localhost:59000 MEMD_S3_BUCKET=memd MEMD_S3_ACCESS_KEY=memd \
  MEMD_S3_SECRET_KEY=memd-secret MEMD_S3_PATH_STYLE=true

make run       # memd all (api + worker in one process) on :50051; MEMD_DSN comes from the Makefile
```

To use a real OpenAI-compatible gateway instead, point `MEMD_GATEWAY_URL` (and `MEMD_GATEWAY_API_KEY`) at it,
copy [memd.example.yaml](memd.example.yaml) to `memd.yaml` and bind its models (see "Model routing" below).

memd has two kinds of settings:

- **Environment variables** (or the matching flags, `MEMD_GRPC_ADDR` = `-grpc-addr`) hold only deployment,
  infrastructure and secrets: the DSN, listen addresses, gateway URL and key, blob storage, worker id and
  slots, log level and format, and Datadog. Run `go run ./cmd/memd` to list them all.
- **The server config file** holds the models and every behaviour and operator setting. memd reads
  `MEMD_CONFIG`, or `./memd.yaml` when that is unset. Its `defaults:` block is the server level of the config
  hierarchy, written like the API's `BankConfig`; the `gateway`, `worker`, `retention`, `api` and `observability`
  sections hold operator settings. [memd.example.yaml](memd.example.yaml) documents every key; unknown keys fail startup.

At startup memd checks the config file, that the embedding model returns 768 dimensions, that the extract and
reflect steps have a model, and that the blob storage is configured. A worker's id is
`<MEMD_WORKER_ID or hostname>-<uuid>`, unique per process; set `MEMD_WORKER_HEALTH_ADDR` to serve grpc.health from
a worker. In production run `memd api` and `memd worker` as two Deployments over the same Postgres, bucket and
config file.
Original document texts go to blob storage. `MEMD_BLOB_PROVIDER` picks the provider; `s3` (the default, and the
only one today) works with AWS S3 and any S3-compatible store such as MinIO:

| Variable | Default | Meaning |
|---|---|---|
| `MEMD_BLOB_PROVIDER` | `s3` | Blob storage provider; any other value fails startup. |
| `MEMD_S3_BUCKET` | (required) | Bucket for original texts; it must exist. |
| `MEMD_S3_ACCESS_KEY`, `MEMD_S3_SECRET_KEY` | (required) | Static credentials (no AWS credential chain). |
| `MEMD_S3_ENDPOINT` | AWS | S3 API URL, e.g. `http://localhost:59000` for the compose MinIO. |
| `MEMD_S3_REGION` | `us-east-1` | Signing region. |
| `MEMD_S3_PATH_STYLE` | `false` | Path-style addressing (MinIO needs `true`). |

Other providers (GCS, Azure Blob Storage) plug in behind the provider-neutral `blob.Store` interface; see
`internal/blob/doc.go` for the steps.

## grpcurl examples

The server registers gRPC reflection, so grpcurl needs no proto files. Every call carries the tenant in the
`x-tenant-id` header.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","items":[{"content":"Alice moved to Paris in May. Alice works as a nurse."}]}' \
  localhost:50051 memory.v1.MemoryService/Retain
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","query":"Where does Alice live?"}' \
  localhost:50051 memory.v1.MemoryService/Recall
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","query":"Summarize what we know about Alice"}' \
  localhost:50051 memory.v1.MemoryService/Reflect
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1"}' localhost:50051 memory.v1.BankService/GetBankStats
```

Memories are curated, never deleted one by one. `ListMemories` finds a fact's id; `UpdateMemory` edits the
fields its `update_mask` names (a named field left unset is cleared), `InvalidateMemory` hides a fact from recall
and `RestoreMemory` brings it back. Observations are not curated directly: they re-derive from their facts.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","text_contains":"Paris"}' \
  localhost:50051 memory.v1.MemoryService/ListMemories
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","memory_id":"<id>","memory":{"text":"Alice moved to Lyon in May."},"update_mask":"text"}' \
  localhost:50051 memory.v1.MemoryService/UpdateMemory
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","memory_id":"<id>","reason":"wrong"}' \
  localhost:50051 memory.v1.MemoryService/InvalidateMemory
```

`ObservationService.ListObservationHistory` pages an observation's versions, newest first.
`ClearObservations` deletes the observations of exact `scopes`, of one `memory_id`, or of the whole bank with
`"all": true`. It must name exactly one target: a request with none is `INVALID_ARGUMENT`. `RetagDocument`
replaces a document's tag set; as a side effect it clears the observations citing the document's memories and
re-consolidates them under the new tags.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","scopes":{"tag_sets":[{"tags":["team"]}]}}' \
  localhost:50051 memory.v1.ObservationService/ClearObservations
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","document_id":"d1","tags":{"tags":["people"]}}' \
  localhost:50051 memory.v1.DocumentService/RetagDocument
```

### API conventions

The header of `api/memory/v1/memory.proto` states these once:

- **Paging.** Every List RPC takes `page_size` (0 = 100, at most 1000, negative = `INVALID_ARGUMENT`) and
  `page_token`, and returns `next_page_token` (empty = done). `ListEvents` pages by sequence: `page_size` plus
  `after_seq`, continuing from the response's `next_after_seq`. `SearchMentalModels` takes `max_results` (0 =
  10, at most 100).
- **Tag filters.** Every request that filters by tag takes one `TagFilter`: flat `tags` under a `match` mode, or
  boolean `groups` (not both). List RPCs accept the flat form only.
- **Async work.** An RPC that enqueues work returns the `Operation` itself (`operation`, `batch_operation`,
  `mental_model_operations`), with its `bank_id`; poll `OperationService.GetOperation`. `debug_summary` is for
  people, not for parsing.
- **Errors.** Every error carries a `google.rpc.ErrorInfo` (domain `memd`) whose `reason` is a stable code such as
  `BANK_DELETING` or `MODEL_UNBOUND`; `INVALID_ARGUMENT` also carries a `google.rpc.BadRequest` naming the
  offending fields, for example `page_size` or `reflect.disposition.empathy`.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","query":"Alice","tags":{"tags":["team"],"match":"TAGS_MATCH_ANY_STRICT"}}' \
  localhost:50051 memory.v1.MemoryService/Recall
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{"bank_id":"b1","after_seq":0,"page_size":50}' \
  localhost:50051 memory.v1.OperationService/ListEvents
```

### Recall arms

Recall runs several retrieval arms concurrently and fuses their ranked lists with RRF. The built-in arms are:

| Arm | Retrieves |
|---|---|
| `vector` | semantic similarity: cosine between the query and memory embeddings (floor 0.3 unless `min_scores.arms.vector` says otherwise) |
| `text` | full-text search: BM25 keyword relevance |
| `graph` | memories linked (semantic and causal links) to the query's best vector matches, up to `recall.graph_max_hops` |
| `temporal` | memories that happened inside the query's explicit or derived time window (skipped without one) |

`ListRecallArms` lists the arms the server has registered, with a description each, so clients can discover
arms added later. A request picks arms with `arms` (empty = the bank's `recall.arms`, else all of them); an
unknown or repeated name is `INVALID_ARGUMENT`. `ReflectRequest.arms` does the same for reflect's retrieval
tools. Banks and tenants set the default set with `recall.arms`, weight arms with `recall.arm_boosts` and give
an arm a retrieval floor with `min_scores.arms`, all keyed by arm name and validated against the registry.

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' -d '{}' localhost:50051 memory.v1.MemoryService/ListRecallArms
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","query":"Who does Alice work with?","arms":["graph","vector"],"trace":true}' \
  localhost:50051 memory.v1.MemoryService/Recall
```

Code: `internal/recall` is the engine (fusion, rerank, boosts, packing, and the re-check that keeps every arm's
hits inside the request's bank and filters), `internal/recall/arms` holds the built-in arms (one file each), and
`internal/recall/armtest` is the contract suite every arm runs. To add an arm, implement `recall.Arm` and register
it in `cmd/memd/wire.go`; the steps are in `internal/recall/doc.go` ("Adding a recall arm").

### Sync vs async retain

`Retain` is synchronous by default. It returns once the units are committed, and the extraction LLM calls
run inside the RPC. A call with no deadline of its own gets `api.rpc_deadline` from the server config file (120s by default).

A sync retain that fails part-way, for example on a deadline or an LLM outage, can leave the document
partly replaced. Retrying it starts from scratch: an inline run never resumes (decision 801). So a document
whose extraction takes longer than the deadline can never complete synchronously.

For long documents, set `run_async: true`. The `retain_document` job resumes from its committed chunks
across retries and worker crashes. You can also give the call a longer client deadline.

## Tests

```sh
make test      # go test -race ./...: store code runs on real Postgres, so this needs Docker or a database URL
make e2e       # compose stack + emulated gateway + memd api/worker processes: every scenario, crash and reclaim
```

Every test that touches the store gets its own database, cloned from a migrated template
(`internal/store/postgres/pgtest`). One ParadeDB container serves the whole `go test ./...` run and is
removed when it ends. The gRPC API is tested through the real services, with only model calls and blob storage
stubbed, on a wired in-process stack (`cmd/memd/stack_test.go`: pgtest, the gateway emulator in
`test/e2e/gatewayemu`, the worker, bufconn), and `internal/grpcapi` keeps only database-free tests (conversion
goldens, interceptors, status mapping, FieldMask patches, health). Environment:

- `MEMD_TEST_DATABASE_URL` (or `DATABASE_URL`): use an existing server instead of a container, e.g.
  `MEMD_TEST_DATABASE_URL=postgres://memd:memd@localhost:55432/memd make test` after `make up`. The user must
  be allowed to create databases; templates are named `memd_tpl_<migrations hash>`, test databases
  `memd_t_<random>` (dropped after each test). The HNSW acceptance test builds its index in parallel when the
  server has enough shared memory (compose sets `shm_size: 1g`) and serially, slower, otherwise.
- `MEMD_REQUIRE_DOCKER=1`: fail, rather than skip, the database and MinIO tests when Docker (or, for the
  database, a URL) is not available (CI sets it).
- `MEMD_ALEMBIC`: the alembic binary that migrates the template (default `.venv/bin/alembic`, built by
  `make tools`).

## Benchmarks

`eval/` runs LoCoMo, LongMemEval and the golden fixtures against a running memd. It downloads the datasets,
writes a report per run, resumes interrupted runs and compares runs. LoCoMo is scored the way Hindsight's
published LoCoMo numbers are, so the two compare directly. [eval/README.md](eval/README.md) has the full flow
against a real AI gateway.

```sh
make bench BENCH=golden ARGS=-stub                  # smoke test, no server
make bench CONFIG=eval/configs/locomo.yaml          # LoCoMo against memd on :50051
make bench-compare A=eval-results/<run> B=eval-results/<run>
```

## Configuration and overrides

One `BankConfig` shape is used at every level and merged server < tenant < bank < consolidation strategy <
request (< item for retain). It is grouped by phase: `retain`, `observations`, `recall`, `reflect`,
`mental_models` and `models`. Scalars: set replaces unset; groups merge field by field; a model binding is used
whole; lists replace. The server level is the `defaults:` block of the server config file; `TenantService` and
`BankService` read and patch the tenant and bank levels with FieldMask paths such as
`observations.dedup_threshold` or `models.extract`, and `ResetBankConfig` makes paths inherit again (`"*"` resets
everything).

Each RPC that does work takes its own overrides message, holding only what that call can change:

| Message | RPCs | Fields |
|---|---|---|
| `RetainOverrides` | Retain (request and item), ReprocessDocument | `retain`, `extract_model`, `time_parse_model` |
| `RecallOverrides` | Recall | `recall` (except `recall.arms`: use the request's `arms`), `time_parse_model`, `rerank_model` |
| `ReflectOverrides` | Reflect | `reflect`, `recall` (except `recall.arms`), `reflect_model`, `structured_output_model`, `rerank_model` |
| `ConsolidateOverrides` | Consolidate | the observations group minus `enabled` / `auto_consolidation` / `reconcile_grace_seconds`, `consolidate_model`, `dedup_check_model` |
| `RederiveOverrides` | UpdateMemory, RestoreMemory | `entity_match_threshold`, `extract_model`, `time_parse_model` |
| `RefreshOverrides` | RefreshMentalModel, MentalModelSpec | `reflect` (including the loop caps `recall_max_tokens` and `search_observations_*`), `max_delta_ratio`, `reflect_model`, `mental_model_model`, `structured_output_model` |

A document keeps its retain overrides (`Document.retain_overrides`) and ReprocessDocument replays them. A mental
model's spec holds its own refresh settings: `schedule` (a oneof: `cron` or `after_consolidation`, plus
`min_interval_seconds`), `scope` (tag filter and fact types) and plain run settings (`mode`, `strategy`, `budget`,
`max_tokens`, ...); `RefreshMentalModelRequest.strategy` overrides the strategy for one call.

## Model routing

Every model-backed step has its own slot: `extract`, `time_parse`, `consolidate`, `dedup_check`, `reflect`,
`structured_output`, `mental_model` (single-shot mental-model refresh), `rerank` and `embed`. `models` binds them
at any level: `models.default` binds every chat step the level leaves unset, `models.<slot>` binds one step. For
each step the most specific level that yields a binding wins, its own slot or its default, and the winning binding
is used whole (model, temperature, max output tokens, reasoning effort, timeout, retries, extra body fields).
`rerank` and `embed` are never filled by a default; without a `rerank` binding recall skips the cross-encoder.
`embed` can only be set in the server config file, and the model must return 768 dimensions.

Swap every step to one model (server config file):

```yaml
defaults:
  models:
    default: {model: gpt-4o-mini}
    rerank: {model: bge-reranker-v2-m3}
```

Use a stronger model for reflect and mental models only, for every bank:

```yaml
defaults:
  models:
    default: {model: gpt-4o-mini}
    reflect: {model: gpt-4o}
    mental_model: {model: gpt-4o}
```

Swap every step for one bank, then one step for one call:

```sh
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","config":{"models":{"default":{"model":"claude-sonnet"}}},"update_mask":"models.default"}' \
  localhost:50051 memory.v1.BankService/UpdateBankConfig
grpcurl -plaintext -H 'x-tenant-id: acme' \
  -d '{"bank_id":"b1","query":"Where does Alice live?","overrides":{"reflect_model":{"model":"gpt-4o","temperature":0}}}' \
  localhost:50051 memory.v1.MemoryService/Reflect
```

The bank's `models.default` beats a step the server file binds by name, so the first call moves every chat step
of `b1` to `claude-sonnet`; the second changes only the reflect step of one call.

## Datadog (optional)

memd can send traces, metrics, logs and (separately) profiles to Datadog through a Datadog agent. It uses
Datadog's own clients directly: dd-trace-go v2 for traces and profiles, and datadog-go v5 (DogStatsD) for
metrics. There is no OpenTelemetry. **It is off by default.** When off, no tracer or metrics client starts, no
instrumentation is installed, no agent is contacted, and logs stay in the text format on stderr.

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
- **Unified service tagging:** `DD_ENV`, `DD_VERSION`, and `DD_SERVICE` (default `memd`). These tag every
  span, metric and log line.
- **Tags:** `DD_TAGS`.
- **Sampling:** `DD_TRACE_SAMPLE_RATE` / `DD_TRACE_SAMPLING_RULES`.
- **Runtime metrics:** `DD_RUNTIME_METRICS_ENABLED` (default on with Datadog).

An unreachable agent never fails startup or a request. Traces are dropped while the tracer keeps retrying.
Metrics are fire-and-forget UDP datagrams. If a client can't start, memd logs it and runs without that part.

### Traces

- **gRPC:** one `grpc.server` span per RPC, with resource = the full method. It is tagged `memd.tenant` and `memd.bank`.
- **Worker jobs:** one `memd.job` span per run, with resource = the kind. It carries the operation id, attempt and outcome.
- **Maintenance:** one `memd.maintenance.tick` span per tick.
- **Postgres:** one `postgres.query` span per statement, recorded only inside a request, job or tick.
- **AI gateway:** one `memd.gateway.call` span per call, tagged with model slot and name, with `http.request` spans under it.
- **S3:** one `S3.request` span per call.

### Metrics

Every metric name starts with `memd.`. Counts are counters. Durations are distributions in milliseconds, so
Datadog computes percentiles across hosts. Sampled values are gauges.

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

Every metric also carries `service`, plus `env` and `version` when `DD_ENV` and `DD_VERSION` are set. Tags are kept low-cardinality on purpose. A metric
never carries a tenant, bank, document or operation id, query text or content; spans carry the ids instead.

The queue gauges come from worker and `all` processes only. Every worker reports the same global numbers, so
graph them with `max by {kind,status}`, not `sum`. A series that empties reads 0 instead of disappearing.

memd does not report counts of memories created or updated. Retain and consolidation don't expose them in a form
the metrics layer can read.

### Logs

With Datadog enabled, memd writes JSON logs to stdout, one object per line. Each line uses Datadog's reserved
attributes: `timestamp`, `status` (`debug`, `info`, `warn` or `error`), `message`, and `logger.name` for the
tracer's own messages. The agent parses these without a pipeline. Every line also carries `dd.service`, plus
`dd.env` and `dd.version` when set. Lines logged during a request, job or tick add `dd.trace_id` and
`dd.span_id`, which link the line to its trace. `MEMD_LOG_FORMAT=json` gives the same JSON with Datadog off, but
on stderr (where memd always logs without Datadog) and without the `dd.*` attributes.

The agent collects logs from the container's output; memd doesn't ship them over HTTP itself. To collect them:

1. Set `DD_LOGS_ENABLED=true` on the agent.
2. Give the agent access to container logs (on Docker, mount the Docker socket).
3. Label the memd container so its logs are tagged as Go logs from service `memd`:
   - **Docker:** the label `com.datadoghq.ad.logs: '[{"source":"go","service":"memd"}]'`.
   - **Kubernetes:** the pod annotation `ad.datadoghq.com/memd.logs: '[{"source":"go","service":"memd"}]'`,
     where `memd` is the container name.

   Alternatively, set `DD_LOGS_CONFIG_CONTAINER_COLLECT_ALL=true` on the agent to collect every container.

This only applies when memd runs in a container. Under `go run`, the logs go to your terminal.

### Running an agent locally

Add this to `docker-compose.yml` or an override file. It takes traces on 8126, metrics on 8125/udp, and the logs
of labelled containers:

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

Then start memd on the host:

```sh
MEMD_DATADOG_ENABLED=true DD_ENV=dev DD_VERSION=$(git rev-parse --short HEAD) go run ./cmd/memd all
```

When memd runs as a container next to the agent, point it at the agent and label it for log collection. The
repository ships no image, so use your own build:

```yaml
  memd:
    image: your-registry/memd
    command: ["all"]
    environment:
      MEMD_DATADOG_ENABLED: "true"
      DD_AGENT_HOST: datadog-agent
      DD_ENV: dev
    labels:
      com.datadoghq.ad.logs: '[{"source":"go","service":"memd"}]'
```

In Kubernetes, set `DD_AGENT_HOST` from the node IP (`status.hostIP`) to reach the agent DaemonSet. That
variable covers both traces and DogStatsD.

Prompts, model output, memory content, request bodies, metadata and SQL parameters are never recorded in spans,
metrics or Datadog tags. Span errors carry only the error class. See
[decision 2400](docs/decisions/2400-datadog-optional.md).

## Contributing

Read [AGENTS.md](AGENTS.md) first: layout, rules and the `make gen && make lint && make test` gate.

The API may change freely until the first release, so `make lint` runs `buf lint` only; after the first release,
`buf breaking` runs against that tag. Proto rules are in [api/AGENTS.md](api/AGENTS.md).

The schema works the same way: until the first release it is one Alembic revision,
`migrations/versions/0001_baseline.py`, edited in place; after it, schema changes are new revisions. See
[migrations/README.md](migrations/README.md).
