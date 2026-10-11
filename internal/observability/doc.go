// Package observability is memd's optional Datadog integration: traces, metrics, logs (JSON with Datadog's reserved
// attributes, correlated with traces), runtime metrics and (separately) continuous profiling. It is built directly
// on Datadog's own clients, dd-trace-go v2 for traces and profiles and datadog-go v5 (DogStatsD) for metrics, with no
// OpenTelemetry SDK or bridge.
//
// # Architecture
//
// It is the only package that imports github.com/DataDog. depguard keeps Datadog out of the services, the
// libraries, internal/domain and the boundary packages (store, ai, blob, clock), and only cmd/memd (main.go and
// wire.go) imports this package. The job runner, the maintenance tick and the gateway and S3 adapters expose
// vendor-free hooks, each nil by default (jobs.Config.TraceJob, maintenance.Deps.TraceTick, gateway.Config.TraceCall
// and OnUsage, s3.Config.APIOptions), and cmd/memd's wiring fills them from a [Telemetry]. The package imports
// internal/domain, internal/store and internal/jobs only for the hook signatures, the queue read, the job outcomes
// and the error classes.
//
//	tel, stop := observability.Start(observability.Config{Enabled: on, Profiler: prof, Metrics: met, Logger: base})
//	defer stop()
//	log := slog.New(tel.LogHandler(handler))   // handler: observability.NewJSONHandler(os.Stdout, ...) or text
//	tel.TracePool(pgxCfg)                       // before pgxpool.NewWithConfig
//	gw := gateway.NewClient(gateway.Config{..., TraceCall: tel.GatewayHook(), OnUsage: tel.UsageHook()},
//		tel.HTTPClient(nil))
//	srvOpts := tel.GRPCServerOptions(grpcapi.TenantFrom)
//	defer tel.StartSampler(ctx, interval, observability.Sources{Queue: maint, Pool: pool.Stat})()
//
// # Switches
//
// Everything is off by default. cmd/memd maps its settings onto [Config]:
//
//   - MEMD_DATADOG_ENABLED=true (flag -datadog-enabled=true) turns Datadog on: [Start] starts the tracer and the
//     DogStatsD client and returns an enabled *Telemetry. Runtime metrics (sent by the tracer) come with it unless
//     DD_RUNTIME_METRICS_ENABLED says otherwise.
//   - MEMD_DATADOG_METRICS (default true) sends memd's own metrics (below); false keeps tracing and runtime metrics
//     but creates no DogStatsD client. Ignored unless Datadog is enabled.
//   - MEMD_DATADOG_PROFILER=true (flag -datadog-profiler=true) also starts the profiler (CPU and heap, the library
//     defaults); it is ignored unless tracing runs.
//   - MEMD_LOG_FORMAT (text or json) picks the log format; unset, it is json with Datadog enabled and text otherwise.
//     cmd/memd also writes logs to stdout instead of stderr with Datadog enabled.
//   - observability.metrics_interval in the server config file (default 15s) is the gauge sampling period.
//
// Disabled, Start returns a nil *Telemetry and a no-op stop func. Every Telemetry method is nil-safe and returns
// "install nothing" (nil server options, nil hooks, the HTTP client and slog handler unchanged, the pgx config
// untouched, a sampler that never starts), so wire.go calls the same code either way, nothing starts, no agent is
// contacted, no statsd client exists and memd behaves exactly as without this package. [Telemetry.Enabled] reports
// which state it is. Enabled, each half degrades on its own: a tracer that fails to start leaves the metrics on (the
// tracing helpers then install nothing), a DogStatsD client that can't be created leaves tracing on, and when both
// fail Start returns nil.
//
// Enabled, the standard Datadog variables are read by the libraries: DD_AGENT_HOST / DD_TRACE_AGENT_PORT or
// DD_TRACE_AGENT_URL (trace agent address), DD_DOGSTATSD_URL (udp://host:port or unix:///path) or DD_AGENT_HOST with
// DD_DOGSTATSD_PORT (DogStatsD; with neither variable set, Start uses localhost:8125 like the tracer's localhost
// default), DD_ENV, DD_SERVICE (default [DefaultService], "memd"; Start passes the service only when DD_SERVICE is
// unset, so the variable wins), DD_VERSION, DD_TAGS, DD_TRACE_SAMPLE_RATE / DD_TRACE_SAMPLING_RULES, and so on.
// DD_ENV, DD_SERVICE and DD_VERSION become the env, service and version tags of every metric (unified service
// tagging). An unreachable agent never fails startup or a request: the tracer logs, drops payloads and retries in the
// background; metrics are UDP (or unix socket) datagrams, buffered and sent from a background worker, so a missing
// agent drops them silently. A tracer, profiler or client start error is logged and memd runs without that part.
// (At start the tracer asks the agent for its features once; a connection refused returns at once, a black-holed
// address can delay startup by up to the tracer's 10 s HTTP timeout.) The tracer's own messages go to the configured
// slog logger, at warn for its errors and warnings, else at info, with logger.name "datadog". The stop func closes
// (and so flushes) the metrics client, then flushes and stops the profiler and the tracer; call it once, at exit,
// after the sampler's stop.
//
// # Traces
//
//   - gRPC server ([Telemetry.GRPCServerOptions]): one "grpc.server" span per RPC, resource = the full method, as the
//     outermost interceptor (it sets grpc.UnaryInterceptor and grpc.StreamInterceptor, so the server only chains its
//     own) so calls the tenant check rejects are traced too; tagged memd.tenant and memd.bank (the request's bank_id)
//     by an inner interceptor that runs after the tenant check. Health checks and reflection are not traced.
//   - Worker jobs ([Telemetry.JobHook] -> jobs.Config.TraceJob): one [SpanJob] span per run, resource = the job
//     kind, tagged memd.operation_id, memd.tenant, memd.bank, memd.job.attempt and memd.job.outcome (completed, retry,
//     failed, released, lost), error unless completed. The handler and the terminal Complete/Fail run under it, so
//     their Postgres and gateway spans nest under the job.
//   - Maintenance ([Telemetry.TickHook] -> maintenance.Deps.TraceTick): one [SpanTick] span per tick.
//   - Postgres ([Telemetry.TracePool]): one [SpanPostgresQuery] span per statement (begin and commit included) under an
//     existing span, resource = the SQL text, tagged db.system and db.row_count. The tracer is memd's own
//     pgx.QueryTracer, not dd-trace-go's pgx contrib: statements outside any span (claim polls, heartbeats, the
//     metrics sampler's queue read) are not traced, so they don't each become a root trace.
//   - AI gateway ([Telemetry.GatewayHook] -> gateway.Config.TraceCall, [Telemetry.HTTPClient]): one [SpanGatewayCall]
//     span per model call (all retries), resource = the route, tagged memd.model.slot and memd.model.name; under it one
//     "http.request" span per HTTP attempt.
//   - S3 ([Telemetry.S3APIOptions] -> s3.Config.APIOptions): one "S3.request" span per S3 call, resource =
//     "S3.<Operation>", tagged by the contrib with the bucket.
//
// # Metrics
//
// Sent over DogStatsD with the namespace [MetricNamespace] ("memd."), so the constants below are the names without
// it. Counts are counters, durations are distributions in milliseconds (global percentiles across hosts), the
// sampled values are gauges:
//
//	grpc.requests, grpc.latency                        method, code      one per unary RPC (not health or reflection),
//	                                                                     outermost, so tenant rejections count too
//	job.started                                        kind              a run began
//	job.completed / retried / failed / released / lost kind              how the run ended (jobs.Outcome*)
//	job.duration                                       kind, outcome     handler and settle
//	gateway.calls, gateway.latency                     slot, model, route   one per model call, retries included
//	gateway.errors                                     + error           error class: unavailable, deadline_exceeded, ...
//	gateway.tokens.input, gateway.tokens.output        slot, model       per successful HTTP call (OnUsage)
//	queue.depth                                        kind, status      pending and processing jobs (gauge)
//	queue.oldest_pending_age_seconds                   kind              oldest due pending job's wait (gauge)
//	postgres.pool.acquired / idle / total / max                          pgxpool.Stat (gauges)
//	postgres.pool.acquires / empty_acquires / canceled_acquires / empty_acquire_wait_ms   growth since the last sample
//
// Tags are bounded on purpose: the full gRPC method, the status code, the job kind and outcome, the model slot and
// model name, the gateway route and the error class. A metric never carries a tenant, bank, document or operation id,
// query text or content (high-cardinality tags are costly in Datadog and would leak ids); those live on the spans.
//
// The gauges come from [Telemetry.StartSampler], which samples [Sources] once at start and then every interval in a
// background goroutine: the pool's statistics in every mode, and the job queue ([QueueReader], store.Maintenance's
// QueueStats: one index scan of the partial index over pending and processing rows, retain_batch parents excluded)
// in worker and all modes only, since the queue is global and api replicas would only repeat it. Every worker
// reports the same queue numbers, so graph them with max (or avg) by kind and status, not sum. A (kind, status)
// series once reported is sent as 0 when it empties, so a drained queue reads 0 rather than going silent. The oldest
// pending age counts only due jobs (run_at in the past), so parked retries and debounced jobs never look stuck.
//
// Not measured: memory activity (units created, observations created or updated). Consolidation reports no
// observation counts in its result and sync Retain does not run as a job, so a job-level count would be partial;
// it would need the services to report counts, which this package does not reach into.
//
// # Logs
//
// [NewJSONHandler] writes one JSON object per line with Datadog's reserved attributes, so the agent parses each
// line without a pipeline: timestamp ([LogKeyTimestamp], RFC 3339), status ([LogKeyStatus], debug/info/warn/error),
// message ([LogKeyMessage]) and logger.name ([LogKeyLogger], for records that name their component, such as the
// tracer's). Other attributes keep their keys. It needs no Datadog client and works with Datadog off
// (MEMD_LOG_FORMAT=json).
//
// [Telemetry.LogHandler] correlates logs with traces while tracing: records logged through slog's *Context methods
// with a span in the context get dd.trace_id and dd.span_id (dd-trace-go's slog contrib; the trace id is 128-bit
// unless DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED=false), and every record gets dd.service, dd.env and dd.version
// ([LogKeyService], [LogKeyEnv], [LogKeyVersion]; the last two when set), which link the line to its service even
// without a span. The worker logs job and tick messages with their context.
//
// Logs are collected by the Datadog agent from the container's output (cmd/memd writes them to stdout with Datadog
// enabled), which is the standard path: there is no agentless HTTP log shipping. The README shows the agent and
// container settings (DD_LOGS_ENABLED, the com.datadoghq.ad.logs label or the Kubernetes annotation).
//
// # What is never recorded
//
// Request or response bodies, gRPC metadata, prompts, messages, tool arguments, model output, memory or document
// content, SQL bind parameters and HTTP headers. Span errors on memd's own spans (job, tick, gateway call, Postgres)
// carry only the error's class: the first of context.Canceled, context.DeadlineExceeded and the domain sentinels
// (domain.ErrCancelled, domain.ErrNotFound, ..., domain.ErrUnavailable) that the error wraps, else a generic "error
// (details in the memd logs)"; gateway.errors tags the same class by name ("other" for the generic one). The message
// is never recorded, because memd's error chains can quote model output or gateway response bodies; the full error
// stays in the memd logs, which carry the trace id. The gRPC span records only the status code of a failed call,
// never its message, while the client still receives the full status; the contrib marks every non-OK code except
// CANCELLED as an error.
//
// Accepted gaps: claim and heartbeat statements are never traced, nor are the readiness probes' checks; streaming
// RPCs (only health Watch and reflection) are not measured.
//
// # Testing
//
// The package tests and cmd/memd's wiring tests (cmd/memd/observability_test.go) use dd-trace-go's mocktracer:
// mocktracer.Start() installs an in-memory global tracer, [New] returns a tracing Telemetry over it without starting
// the real tracer, and FinishedSpans() exposes names, tags and parent ids to assert on. Metrics tests use
// [NewMetrics] with a fake statsd.ClientInterface that records each sample; the real client is checked once against a
// local UDP listener (namespace and unified service tags). Tracer state is global, so those tests never run in
// parallel. cmd/memd's TestDatadogUnreachableAgentStillServes starts the real tracer, profiler and DogStatsD client
// against an unreachable agent (a closed local port) to show memd still serves. The SPEC says nothing about
// observability; the design above is the whole contract.
package observability
