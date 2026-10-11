// Package observability is memd's optional Datadog integration: tracing, log correlation, runtime metrics
// and (separately) continuous profiling, built directly on dd-trace-go v2 (no OpenTelemetry SDK or bridge).
// It is the only package that imports github.com/DataDog; the engines, domain and ports never see it
// (depguard enforces that), and the worker and the gateway and S3 adapters only expose vendor-free hooks
// that cmd/memd/wire.go fills from here.
//
// # Switches
//
// Everything is off by default. cmd/memd maps its settings onto Config:
//
//   - MEMD_DATADOG_ENABLED=true (flag -datadog-enabled=true) turns tracing on: Start starts the tracer and
//     returns an enabled *Telemetry. Runtime metrics (DogStatsD) come with it unless
//     DD_RUNTIME_METRICS_ENABLED says otherwise.
//   - MEMD_DATADOG_PROFILER=true (flag -datadog-profiler=true) also starts the profiler; it is ignored
//     unless tracing is enabled.
//
// Disabled, Start returns a nil *Telemetry and a no-op stop func. Every Telemetry method is nil-safe and
// returns "install nothing" (nil server options, nil hooks, the HTTP client and slog handler unchanged,
// the pgx config untouched), so wire.go calls the same code either way, nothing starts, no agent is
// contacted and memd behaves exactly as without this package.
//
// Enabled, the standard Datadog variables are read by the library: DD_AGENT_HOST / DD_TRACE_AGENT_PORT or
// DD_TRACE_AGENT_URL (agent address), DD_ENV, DD_SERVICE (default "memd"), DD_VERSION, DD_TAGS,
// DD_TRACE_SAMPLE_RATE / DD_TRACE_SAMPLING_RULES, DD_DOGSTATSD_PORT, and so on. An unreachable agent never
// fails startup or a request: the tracer logs, drops payloads and retries in the background. (At start
// the tracer asks the agent for its features once; a connection refused returns at once, a black-holed
// address can delay startup by up to the tracer's 10 s HTTP timeout.)
//
// # What is traced
//
//   - gRPC server (GRPCServerOptions): one "grpc.server" span per RPC, resource = the full method, as the
//     outermost interceptor so rejected calls are traced too; tagged memd.tenant and memd.bank (the
//     request's bank_id). Health checks and reflection are not traced.
//   - Worker jobs (JobHook -> worker.RunnerConfig.TraceJob): one "memd.job" span per run, resource = the
//     job kind, tagged memd.operation_id, memd.tenant, memd.bank, memd.job.attempt and memd.job.outcome
//     (completed, retry, failed, released, lost), error unless completed. The handler and the terminal
//     Complete/Fail run under it, so their Postgres and gateway spans nest under the job.
//   - Maintenance (TickHook -> worker.MaintenanceDeps.TraceTick): one "memd.maintenance.tick" span per tick.
//   - Postgres (TracePool): one "postgres.query" span per statement under an existing span, resource = the
//     SQL text, tagged db.system and db.row_count. Statements outside any span (claim polls, heartbeats)
//     are not traced.
//   - AI gateway (GatewayHook -> gateway.Config.TraceCall, HTTPClient): one "memd.gateway.call" span per
//     model call (all retries), resource = the route, tagged memd.model.slot and memd.model.name; under it
//     one "http.request" span per HTTP attempt.
//   - S3 (S3APIOptions -> s3.Config.APIOptions): one "S3.request" span per S3 call, resource =
//     "S3.<Operation>", tagged with the bucket.
//   - Logs (LogHandler): records logged through slog's *Context methods with a span in the context get
//     dd.trace_id and dd.span_id. The worker logs job and tick messages that way.
//
// # What is never tagged
//
// Request or response bodies, gRPC metadata, prompts, messages, tool arguments, model output, memory or
// document content, SQL bind parameters and HTTP headers. Span errors carry only the error's class
// ("unavailable", "not found", context.Canceled, ...), never its message, because memd's error chains can
// quote model output or gateway response bodies; the full error stays in the memd logs, which carry the
// trace id. The gRPC span's error is the status the client also receives.
//
// # Testing
//
// The package tests and cmd/memd's wiring tests use dd-trace-go's mocktracer: mocktracer.Start() installs
// an in-memory global tracer, New(log) returns an enabled Telemetry over it without starting the real
// tracer, and FinishedSpans() exposes names, tags and parent ids to assert on. Tracer state is global, so
// those tests never run in parallel. Start itself is tested against an unreachable agent (a closed local
// port) to show memd still serves.
package observability
