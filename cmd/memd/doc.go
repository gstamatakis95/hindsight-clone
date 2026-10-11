// Command memd is the agent-memory gRPC service. One binary runs in one of three modes, chosen by its first
// argument:
//
//	memd api    [flags]   gRPC server: Recall, Reflect, sync Retain and every admin RPC
//	memd worker [flags]   the job runner and the maintenance tick
//	memd all    [flags]   both in one process
//
// # Where it sits
//
// cmd/memd is the composition root, the only place concrete types meet. wire.go opens the infrastructure (the
// Postgres pool and postgres.DB, the blob store MEMD_BLOB_PROVIDER selects, the AI gateway client), narrows each to
// its boundary (store, graph, blob, ai), builds the shared libraries once (config resolver, ai.Router, ai.Tokenizer,
// timex, linking, retain's shared retainer, recall with the arms recallArms registers, reflect) and runs the two
// modes. Each wire_<area>.go (memory, document, observation, mentalmodel, bank, config, operation, maintenance)
// builds one area's engines or service, its gRPC handlers (internal/grpcapi), its job handlers (handlers.go adapts
// the engines to jobs.Handler for the internal/jobs runner and registers a service's own RunXJob) and its part of the
// maintenance service (internal/maintenance). config.go reads the environment and flags, configfile.go the
// server config file, workerid.go the worker id, and glue.go the small adapter no engine provides yet: the
// operation reader. The readiness probes and the worker's health server live in
// internal/grpcapi. Engines never import this package; nothing here holds business logic.
//
// # Startup
//
//  1. LoadConfig reads every setting from the environment, then from flags after the mode (a flag wins), then the
//     server config file (configfile.go). Validate checks the mode, the required settings and the file: its
//     defaults block (config.ServerDefaults and config.ValidateServer), that EXTRACT and REFLECT have a model
//     (defaults.models.default or defaults.models.<slot>), positive operator settings and a heartbeat below 3/4 of
//     the reclaim threshold, so a bad deployment fails before anything connects. A missing or unknown mode prints
//     the usage and exits 2; a bad flag or value, an unknown key in the file, extra arguments or a failed
//     validation prints the error and exits 1.
//  2. Optional Datadog instrumentation starts (internal/observability); disabled, it installs nothing.
//  3. The adapters open: Postgres (pinged within 30 s), blob storage and the gateway. The embedding model
//     (defaults.models.embed, default nomic-embed-text-v1.5) must return 768 dimensions (memory_units.embedding is
//     vector(768)), else startup fails.
//  4. The tokenizer (tiktoken cl100k_base, embedded) and the server level of the config hierarchy are built, then
//     every engine.
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
// The environment holds only deployment, infrastructure and secrets. Every variable has a flag of the same name,
// lower case, without the MEMD_ prefix and with dashes (MEMD_GRPC_ADDR = -grpc-addr). Defaults:
//
//	MEMD_DSN                  Postgres URL; required. A SQLAlchemy driver suffix ("postgresql+psycopg://") is
//	                          dropped, so the Alembic DSN works too.
//	MEMD_CONFIG               ""         server config file; empty = ./memd.yaml when it exists
//	MEMD_GRPC_ADDR            ":50051"   gRPC listen address (api, all)
//	MEMD_GATEWAY_URL          AI gateway base URL including /v1; required
//	MEMD_GATEWAY_API_KEY      ""         bearer key sent to the gateway
//	MEMD_BLOB_PROVIDER        "s3"       blob storage for original texts; s3 is the only provider
//	MEMD_S3_ENDPOINT          ""         S3 API URL; empty = AWS for the region
//	MEMD_S3_BUCKET            bucket for original document texts; required
//	MEMD_S3_ACCESS_KEY        static access key; required (no AWS credential chain)
//	MEMD_S3_SECRET_KEY        static secret key; required
//	MEMD_S3_REGION            "us-east-1"   signing region
//	MEMD_S3_PATH_STYLE        false      path-style addressing (MinIO)
//	MEMD_WORKER_ID            ""         worker id prefix; empty = hostname
//	MEMD_WORKER_SLOTS         10         jobs a worker runs at once (2 reserved for consolidate)
//	MEMD_WORKER_HEALTH_ADDR   ""         where the worker serves grpc.health.v1; empty = nowhere
//	MEMD_DB_MAX_CONNS         0          Postgres pool size; 0 = max(2 x worker slots + 16, 32)
//	MEMD_LOG_LEVEL            "info"     debug, info, warn or error
//	MEMD_DATADOG_ENABLED      false      Datadog tracing, log correlation and runtime metrics
//	MEMD_DATADOG_PROFILER     false      Datadog continuous profiler; only with Datadog enabled
//
// With MEMD_DATADOG_ENABLED the standard Datadog variables apply, read by dd-trace-go or
// internal/observability: DD_AGENT_HOST, DD_TRACE_AGENT_PORT or DD_TRACE_AGENT_URL, DD_SERVICE (default
// "memd"), DD_ENV, DD_VERSION, DD_TAGS, DD_RUNTIME_METRICS_ENABLED (default on), sampling settings and so on.
// An unreachable agent never fails startup. The usage text (memd with no mode) lists the same settings.
//
// # Server config file
//
// Models and every behaviour or operator setting live in a YAML file (memd.example.yaml documents every key;
// unknown keys are errors):
//
//	defaults:    the server level of the config hierarchy, shaped like BankConfig (retain, observations, recall,
//	             reflect, mental_models, models). models.default binds every chat step, models.<slot> one step,
//	             models.rerank the cross-encoder (unbound = no rerank), models.embed the embedding model.
//	gateway:     max_concurrent (32), slot_concurrency (per slot, e.g. {extract: 8}), timeout (120s)
//	worker:      poll_interval (500ms), heartbeat_interval (15s), stale_after (2m), maintenance_interval (60s)
//	retention:   operations (168h), events (720h)
//	api:         rpc_deadline (120s; 0s = none)
//
// # Wiring details
//
//   - operationReader serves GetOperation, ListOperations and ListEvents in short read-only transactions; an
//     unknown or tombstoned bank is NOT_FOUND. ListEvents with limit 0 returns at most 100 events, and any
//     limit is capped at 1000 (the store's own List treats 0 as unlimited).
//   - Readiness probes: Postgres is pinged; the gateway is reachable when a GET of its base URL gets any HTTP
//     response (no model call; the result is cached for 10 s).
//   - The maintenance tick's cross-bank scans (unconsolidated banks, stalled deletes, banks with graph work,
//     stranded facts, retain_batch settlement, cron models, failed refreshes) are store.Maintenance, which
//     postgres.DB implements.
//   - With Datadog on, the pgx pool, S3 client, gateway client, gRPC server, job runs (jobs.Config.TraceJob)
//     and maintenance ticks (maintenance.Deps.TraceTick) are traced; gRPC and job spans carry the tenant as a tag.
//
// # Tests
//
// config_test.go covers settings from the environment and flags, validation (the blob provider and S3 keys
// included), the server config file (its sections, required slots, unknown keys and bad values, the default
// path), the worker id, and the embedding check. stack_test.go is the shared test stack: the fully wired API and
// worker over a pgtest database, an in-memory blob store and the gateway emulator, on bufconn. On it,
// wire_test.go checks that no model call happens inside a store transaction, and api_test.go checks the API's
// behaviour through the real engines (every RPC wired, config and spec-override validation, document retags,
// reflect's based_on); handlers_test.go pins each job's result keys and delete_bank's
// continuations; glue_test.go checks ListEvents' default and cap; and
// observability_test.go covers the Datadog wiring (disabled installs nothing, spans nest, an unreachable agent
// still serves). End-to-end tests (test/e2e) run the built binary against Postgres, MinIO and a gateway
// emulator using these variable names. SPEC "Process model", "Model routing" and "File structure".
package main
