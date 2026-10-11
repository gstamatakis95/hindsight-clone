// Command memd is the agent-memory gRPC service. One binary runs in one of three modes, chosen by its first
// argument:
//
//	memd api    [flags]   gRPC server: Recall, Reflect, sync Retain and every admin RPC
//	memd worker [flags]   the job runner and the maintenance tick
//	memd all    [flags]   both in one process
//
// # Where it sits
//
// cmd/memd is the composition root (domain -> port -> engine -> server/worker -> cmd/memd). wire.go is the
// only place concrete types meet: it opens the adapters (Postgres pool and store, S3 blob store, AI gateway
// client), narrows each to the ports the engines consume, builds every engine once, and hands them to the
// gRPC server (internal/server) and to the job handlers and maintenance tick (internal/worker). glue.go holds
// the small adapters no engine provides: the system clock, slotRouter, the worker id, the worker's health
// server, the operation reader, the reflect page reader and the readiness probes. Engines never import this
// package; nothing here holds business logic.
//
// # Startup
//
//  1. LoadConfig reads every setting from the environment, then from flags after the mode (a flag wins).
//     Validate checks the mode, the required settings, positive timings, a heartbeat below 3/4 of the
//     reclaim threshold, and that EXTRACT and REFLECT have a model (MEMD_MODEL or MEMD_MODEL_<SLOT>), so a
//     bad deployment fails before anything connects. A missing or unknown mode prints the usage and exits 2;
//     a bad flag or value, extra arguments or a failed validation prints the error and exits 1.
//  2. Optional Datadog instrumentation starts (internal/observability); disabled, it installs nothing.
//  3. The adapters open: Postgres (pinged within 30 s), S3 and the gateway. The gateway's embedding model
//     must return 768 dimensions (memory_units.embedding is vector(768)), else startup fails.
//  4. The token counter (tiktoken cl100k_base, embedded) and the server-level config (config.ServerDefaults)
//     are built, then every engine.
//  5. The mode runs until SIGINT or SIGTERM. The api stops gracefully within 20 s; the worker stops claiming,
//     lets running jobs finish within the runner's shutdown grace and releases the rest. In mode all an error
//     in either half stops both.
//
// The api also registers gRPC server reflection, so grpcurl can discover the services. The worker's id is
// "<MEMD_WORKER_ID or hostname>-<uuid>", fresh per process, so two workers given the same MEMD_WORKER_ID
// never share an id and heartbeat fencing keeps them apart.
//
// # Settings
//
// Every setting is an environment variable with a flag of the same name, lower case, without the MEMD_ prefix
// and with dashes (MEMD_GRPC_ADDR = -grpc-addr). Defaults:
//
//	MEMD_DSN                            Postgres URL; required. A SQLAlchemy driver suffix
//	                                    ("postgresql+psycopg://") is dropped, so the Alembic DSN works too.
//	MEMD_GRPC_ADDR                      ":50051"   gRPC listen address (api, all)
//	MEMD_GATEWAY_URL                    AI gateway base URL including /v1; required
//	MEMD_GATEWAY_API_KEY                ""         bearer key sent to the gateway
//	MEMD_EMBED_MODEL                    "nomic-embed-text-v1.5"   server-wide embedding model (768 dims)
//	MEMD_GATEWAY_MAX_CONCURRENT         32         in-flight gateway calls in this process
//	MEMD_GATEWAY_TIMEOUT                120s       one gateway HTTP attempt, when the binding sets none
//	MEMD_S3_ENDPOINT                    ""         S3 API URL; empty = AWS for the region
//	MEMD_S3_BUCKET                      bucket for original document texts; required
//	MEMD_S3_ACCESS_KEY                  static access key; required (no AWS credential chain)
//	MEMD_S3_SECRET_KEY                  static secret key; required
//	MEMD_S3_REGION                      "us-east-1"   signing region
//	MEMD_S3_PATH_STYLE                  false      path-style addressing (MinIO)
//	MEMD_WORKER_ID                      ""         worker id prefix; empty = hostname
//	MEMD_WORKER_SLOTS                   10         jobs a worker runs at once (2 reserved for consolidate)
//	MEMD_POLL_INTERVAL                  500ms      worker claim period
//	MEMD_HEARTBEAT_INTERVAL             15s        worker heartbeat period; must be below 3/4 of STALE_AFTER
//	MEMD_STALE_AFTER                    2m         heartbeat age after which a job is reclaimed
//	MEMD_MAINTENANCE_INTERVAL           60s        maintenance tick period
//	MEMD_CONSOLIDATION_RECONCILE_GRACE  10m        age of an unconsolidated fact before the tick consolidates
//	MEMD_WORKER_HEALTH_ADDR             ""         where the worker serves grpc.health.v1; empty = nowhere
//	MEMD_RPC_DEADLINE                   120s       deadline of calls that carry none; 0 = none
//	MEMD_DB_MAX_CONNS                   0          Postgres pool size; 0 = max(2 x worker slots + 16, 32)
//	MEMD_LOG_LEVEL                      "info"     debug, info, warn or error
//	MEMD_DATADOG_ENABLED                false      Datadog tracing, log correlation and runtime metrics
//	MEMD_DATADOG_PROFILER               false      Datadog continuous profiler; only with Datadog enabled
//
// Read from the environment only (no flag):
//
//	MEMD_MODEL                          gateway model of every LLM slot (all but RERANK and EMBED)
//	MEMD_MODEL_<SLOT>                   model of one slot (EXTRACT, TIME_PARSE, CONSOLIDATE, DEDUP_CHECK, REFLECT,
//	                                    STRUCTURED_OUTPUT, MENTAL_MODEL_DELTA, SYNTHESIZE, RERANK); wins over
//	                                    MEMD_MODEL. RERANK is bound only this way; without it recall skips the
//	                                    cross-encoder. EXTRACT and REFLECT must be bound.
//	MEMD_GATEWAY_MAX_CONCURRENT_<SLOT>  in-flight gateway calls of one slot (any slot above, or EMBED); must be
//	                                    positive; unset = only the process-wide cap applies
//
// With MEMD_DATADOG_ENABLED the standard Datadog variables apply, read by dd-trace-go or
// internal/observability: DD_AGENT_HOST, DD_TRACE_AGENT_PORT or DD_TRACE_AGENT_URL, DD_SERVICE (default
// "memd"), DD_ENV, DD_VERSION, DD_TAGS, DD_RUNTIME_METRICS_ENABLED (default on), sampling settings and so on.
// An unreachable agent never fails startup. The usage text (memd with no mode) lists the same settings.
//
// # Wiring details
//
//   - slotRouter wraps the engine router: a slot with no model bound is FAILED_PRECONDITION (the deployment
//     or bank config lacks a binding, the request is fine) and a permanent job failure, naming the variables
//     to set; EMBED and the unspecified slot keep the router's INVALID_ARGUMENT.
//   - operationReader serves GetOperation, ListOperations and ListEvents in short read-only transactions; an
//     unknown or tombstoned bank is NOT_FOUND. ListEvents with limit 0 returns at most 100 events, and any
//     limit is capped at 1000 (the store's own List treats 0 as unlimited).
//   - Readiness probes: Postgres is pinged; the gateway is reachable when a GET of its base URL gets any HTTP
//     response (no model call; the result is cached for 10 s).
//   - The worker's maintenance reads (unconsolidated banks, stalled deletes, banks with graph work, stranded
//     facts, retain_batch settlement) are Postgres store methods narrowed to the worker's interfaces.
//   - With Datadog on, the pgx pool, S3 client, gateway client, gRPC server, job runs (worker TraceJob) and
//     maintenance ticks (TraceTick) are traced; gRPC and job spans carry the tenant as a tag.
//
// # Tests
//
// config_test.go covers settings from the environment and flags, validation, required slots, the unbound-slot
// router, per-slot concurrency, S3 keys, the worker id and health server, and the embedding and tokenizer
// checks; wire_test.go runs the fully wired stack over a pgtest database and the model fakes and checks that no model
// call happens inside a store transaction; glue_test.go checks ListEvents' default and cap; and
// observability_test.go covers the Datadog wiring (disabled installs nothing, spans nest, an unreachable agent
// still serves). End-to-end tests (test/e2e) run the built binary against Postgres, MinIO and a gateway
// emulator using these variable names. SPEC "Process model", "Model routing" and "File structure".
package main
