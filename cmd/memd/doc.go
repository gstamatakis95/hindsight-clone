// Command memd is the agent-memory gRPC service. One binary runs in one of four modes, chosen by its first
// argument:
//
//	memd api    [flags]   gRPC server: Recall, Reflect, sync Retain and every admin RPC
//	memd worker [flags]   the job runner and the maintenance tick
//	memd ingest [flags]   the queue consumer: retains the messages of the MEMD_INGEST_PROVIDER source
//	memd all    [flags]   api and worker in one process, plus the queue consumer when a provider is set
//
// # Where it sits
//
// cmd/memd is the composition root, the only place concrete types meet. wire.go opens the infrastructure (the Postgres
// pool and postgres.DB, the blob store MEMD_BLOB_PROVIDER selects, the AI gateway client), narrows each to its boundary
// (store, graph, blob, ai) and runs the modes. wire_services.go builds the shared libraries once (config resolver,
// ai.Router, ai.Tokenizer, timex, linking, recall with the arms recallArms registers, reflect), then one wire<Area>
// function per service (memory, document, observation, mentalmodel, bank, config, operation, maintenance) builds it,
// hands it to its gRPC handlers (internal/grpcapi), registers its job handlers (Service.RunXJob) for the internal/jobs
// runner and sets its steps of the maintenance tick; wireMaintenance builds maintenance.Service last, with the
// graph_maintenance handler. wire_ingest.go builds the ingest.Source MEMD_INGEST_PROVIDER names (Kafka) and the
// ingestapi.Consumer over it, the memory service (RetainBatch) and the org service (deletion receipts). config.go
// reads the environment and flags and derives the worker id, configfile.go reads the server config file. The
// readiness probes and the health server live in internal/grpcapi. No service imports this package; nothing here
// holds business logic.
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
//  2. The logger is built (text on stderr; with Datadog enabled JSON on stdout, see MEMD_LOG_FORMAT) and the
//     optional Datadog instrumentation starts (internal/observability); disabled, it installs nothing.
//  3. The adapters open: Postgres (pinged within 30 s), blob storage and the gateway. The embedding model
//     (defaults.models.embed, default nomic-embed-text-v1.5) must return 768 dimensions (memory_units.embedding is
//     vector(768)), else startup fails; memd ingest makes no model call, so it skips the check and needs no
//     gateway.
//  4. The tokenizer (tiktoken cl100k_base, embedded) and the server level of the config hierarchy are built, then
//     every service.
//  5. The mode runs until SIGINT or SIGTERM. The api stops gracefully within 20 s; the worker stops claiming,
//     lets running jobs finish within the runner's shutdown grace and releases the rest; the queue consumer stops
//     receiving, retains and settles what it holds within ingest.consumer.shutdown_grace and closes the source. In
//     mode all an error in one part stops them all.
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
//	MEMD_LOG_FORMAT           ""         text or json; empty = json with Datadog enabled, else text
//	MEMD_DATADOG_ENABLED      false      Datadog tracing, metrics, runtime metrics and log correlation
//	MEMD_DATADOG_PROFILER     false      Datadog continuous profiler; only with Datadog enabled
//	MEMD_DATADOG_METRICS      true       memd's own metrics over DogStatsD; only with Datadog enabled
//	MEMD_INGEST_PROVIDER      "none"     queue consumer source: none or kafka; ingest needs one, all runs the
//	                          consumer when one is set
//	MEMD_INGEST_TENANT        ""         pin the queue to one tenant; empty = every envelope names its tenant
//	MEMD_INGEST_ORG           ""         pin the queue to one org; empty = every envelope names its org
//	MEMD_INGEST_HEALTH_ADDR   ""         where memd ingest serves grpc.health.v1; empty = nowhere
//	MEMD_INGEST_KAFKA_BROKERS ""         seed brokers, comma-separated host:port; required with kafka
//	MEMD_INGEST_KAFKA_TOPIC   ""         topic to consume; required with kafka
//	MEMD_INGEST_KAFKA_GROUP   "memd-ingest"   consumer group of every memd ingest process
//	MEMD_INGEST_KAFKA_DLQ_TOPIC ""       dead-letter topic; required with kafka, and it must exist
//	MEMD_INGEST_KAFKA_CLIENT_ID ""       client id; empty = memd
//	MEMD_INGEST_KAFKA_TLS     false      dial over TLS (implied by a TLS file); _TLS_CA_FILE, _TLS_CERT_FILE,
//	                          _TLS_KEY_FILE are PEM files
//	MEMD_INGEST_KAFKA_SASL_MECHANISM ""  PLAIN, SCRAM-SHA-256 or SCRAM-SHA-512, with _SASL_USERNAME and
//	                          _SASL_PASSWORD
//
// Logs go to stderr, except with MEMD_DATADOG_ENABLED, when they go to stdout (the stream the Datadog agent
// collects without marking lines as errors). The json format (observability.NewJSONHandler) writes one object per
// line with Datadog's reserved attributes (timestamp, status, message, logger.name), so the agent parses it without
// a pipeline; an explicit MEMD_LOG_FORMAT wins either way, and with Datadog off the default is the text format on
// stderr.
//
// With MEMD_DATADOG_ENABLED the standard Datadog variables apply, read by dd-trace-go, datadog-go or
// internal/observability: DD_AGENT_HOST, DD_TRACE_AGENT_PORT or DD_TRACE_AGENT_URL (traces), DD_DOGSTATSD_URL or
// DD_AGENT_HOST with DD_DOGSTATSD_PORT (metrics; default localhost:8125), DD_SERVICE (default "memd"), DD_ENV,
// DD_VERSION, DD_TAGS, DD_RUNTIME_METRICS_ENABLED (default on), sampling settings and so on. An unreachable agent
// never fails startup. The usage text (memd with no mode) lists the same settings.
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
//	ingest:      request limits and the backlog gate; ingest.consumer: batch_size (100), flush_interval (500ms),
//	             concurrency (4), max_attempts (10), backoff_initial (1s), backoff_max (5m), shutdown_grace (20s),
//	             poll_wait (1s), max_buffered (10000)
//	observability: metrics_interval (15s): how often the Datadog pool and queue gauges are sampled
//
// # Wiring details
//
//   - Readiness probes: Postgres is pinged; the gateway is reachable when a GET of its base URL gets any HTTP
//     response (no model call; the result is cached for 10 s on the injected clock). memd ingest's health
//     (MEMD_INGEST_HEALTH_ADDR) probes Postgres only.
//   - The maintenance tick's cross-bank scans (unconsolidated banks, stalled deletes, banks with graph work,
//     stranded facts, retain_batch settlement, cron models, failed refreshes) are store.Maintenance, which
//     postgres.DB implements.
//   - With Datadog on, the pgx pool, S3 client, gateway client, gRPC server, job runs (jobs.Config.TraceJob),
//     maintenance ticks (maintenance.Deps.TraceTick) and the consumer's RetainBatch calls (ingestapi.Hooks) are
//     traced; gRPC, job and ingest spans carry the tenant as a tag. The same gRPC interceptor, job hook, gateway
//     hooks (TraceCall, OnUsage) and consumer hooks send the metrics, whose tags never name a tenant, bank or
//     operation. run starts the gauge sampler (observability.Telemetry.StartSampler)
//     after buildDeps and stops it before the pool closes: the pool's statistics in every mode, the job queue
//     (store.Maintenance.QueueStats) in worker and all modes only, since the queue is global.
//
// # Tests
//
// config_test.go covers settings from the environment and flags, validation (the blob provider and S3 keys
// included), the server config file (its sections, required slots, unknown keys and bad values, the default
// path), the worker id, and the embedding check. stack_test.go is the shared test stack: the fully wired API and
// worker over a pgtest database, an in-memory blob store and the gateway emulator, on bufconn. On it,
// wire_test.go checks that no model call happens inside a store transaction, and api_test.go checks the API's
// behaviour through the real services (every RPC wired, config and spec-override validation, document retags,
// reflect's based_on); and observability_test.go covers the Datadog wiring (disabled installs nothing, spans nest,
// a retain emits the gRPC, gateway and job metrics with bounded tags, an unreachable agent still serves) and the
// log format and stream switch. ingest_test.go delivers one queue message twice on the wired stack and checks it
// is retained once. End-to-end tests (test/e2e) run the built binary against Postgres, MinIO and a
// gateway emulator using these variable names. Design: docs/design/architecture.md#process-model and
// docs/design/configuration.md.
package main
