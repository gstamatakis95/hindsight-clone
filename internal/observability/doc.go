// Package observability is memd's optional Datadog integration: tracing, log correlation, runtime metrics and
// (separately) continuous profiling, built directly on dd-trace-go v2 (no OpenTelemetry SDK or bridge).
//
// # Architecture
//
// It is the only package that imports github.com/DataDog. depguard keeps Datadog out of internal/engine,
// internal/domain and internal/port, and only cmd/memd (main.go and wire.go) imports this package. The worker and the
// gateway and S3 adapters expose vendor-free hooks, each nil by default (worker.RunnerConfig.TraceJob,
// worker.MaintenanceDeps.TraceTick, gateway.Config.TraceCall, s3.Config.APIOptions), and cmd/memd/wire.go fills them
// from a [Telemetry]. The package imports internal/domain and internal/port only for the hook signatures and the error
// classes.
//
//	tel, stop := observability.Start(observability.Config{Enabled: on, Profiler: prof, Logger: base})
//	defer stop()
//	log := slog.New(tel.LogHandler(handler))
//	tel.TracePool(pgxCfg)                     // before pgxpool.NewWithConfig
//	gw := gateway.NewClient(gateway.Config{..., TraceCall: tel.GatewayHook()}, tel.HTTPClient(nil))
//	srvOpts := tel.GRPCServerOptions(server.TenantFrom)
//
// # Switches
//
// Everything is off by default. cmd/memd maps its settings onto [Config]:
//
//   - MEMD_DATADOG_ENABLED=true (flag -datadog-enabled=true) turns tracing on: [Start] starts the tracer and returns an
//     enabled *Telemetry. Runtime metrics (DogStatsD) come with it unless DD_RUNTIME_METRICS_ENABLED says otherwise.
//   - MEMD_DATADOG_PROFILER=true (flag -datadog-profiler=true) also starts the profiler (CPU and heap, the library
//     defaults); it is ignored unless tracing is enabled.
//
// Disabled, Start returns a nil *Telemetry and a no-op stop func. Every Telemetry method is nil-safe and returns
// "install nothing" (nil server options, nil hooks, the HTTP client and slog handler unchanged, the pgx config
// untouched), so wire.go calls the same code either way, nothing starts, no agent is contacted and memd behaves exactly
// as without this package. [Telemetry.Enabled] reports which state it is.
//
// Enabled, the standard Datadog variables are read by the library: DD_AGENT_HOST / DD_TRACE_AGENT_PORT or
// DD_TRACE_AGENT_URL (agent address), DD_ENV, DD_SERVICE (default [DefaultService], "memd"; Start passes the service
// option only when DD_SERVICE is unset, so the variable wins), DD_VERSION, DD_TAGS, DD_TRACE_SAMPLE_RATE /
// DD_TRACE_SAMPLING_RULES, DD_DOGSTATSD_PORT, and so on. An unreachable agent never fails startup or a request: the
// tracer logs, drops payloads and retries in the background. A tracer or profiler start error is logged and memd runs
// untraced. (At start the tracer asks the agent for its features once; a connection refused returns at once, a
// black-holed address can delay startup by up to the tracer's 10 s HTTP timeout.) The tracer's own messages go to the
// configured slog logger, at warn for its errors and warnings, else at info. The stop func flushes and stops the
// profiler and the tracer; call it once, at exit.
//
// # What is traced
//
//   - gRPC server ([Telemetry.GRPCServerOptions]): one "grpc.server" span per RPC, resource = the full method, as the
//     outermost interceptor (it sets grpc.UnaryInterceptor and grpc.StreamInterceptor, so the server only chains its
//     own) so calls the tenant check rejects are traced too; tagged memd.tenant and memd.bank (the request's bank_id)
//     by an inner interceptor that runs after the tenant check. Health checks and reflection are not traced.
//   - Worker jobs ([Telemetry.JobHook] -> worker.RunnerConfig.TraceJob): one [SpanJob] span per run, resource = the job
//     kind, tagged memd.operation_id, memd.tenant, memd.bank, memd.job.attempt and memd.job.outcome (completed, retry,
//     failed, released, lost), error unless completed. The handler and the terminal Complete/Fail run under it, so
//     their Postgres and gateway spans nest under the job.
//   - Maintenance ([Telemetry.TickHook] -> worker.MaintenanceDeps.TraceTick): one [SpanTick] span per tick.
//   - Postgres ([Telemetry.TracePool]): one [SpanPostgresQuery] span per statement (begin and commit included) under an
//     existing span, resource = the SQL text, tagged db.system and db.row_count. The tracer is memd's own
//     pgx.QueryTracer, not dd-trace-go's pgx contrib: statements outside any span (claim polls, heartbeats) are not
//     traced, so they don't each become a root trace.
//   - AI gateway ([Telemetry.GatewayHook] -> gateway.Config.TraceCall, [Telemetry.HTTPClient]): one [SpanGatewayCall]
//     span per model call (all retries), resource = the route, tagged memd.model.slot and memd.model.name; under it one
//     "http.request" span per HTTP attempt.
//   - S3 ([Telemetry.S3APIOptions] -> s3.Config.APIOptions): one "S3.request" span per S3 call, resource =
//     "S3.<Operation>", tagged by the contrib with the bucket.
//   - Logs ([Telemetry.LogHandler]): records logged through slog's *Context methods with a span in the context get
//     dd.trace_id and dd.span_id. The worker logs job and tick messages that way.
//
// # What is never tagged
//
// Request or response bodies, gRPC metadata, prompts, messages, tool arguments, model output, memory or document
// content, SQL bind parameters and HTTP headers. Span errors on memd's own spans (job, tick, gateway call, Postgres)
// carry only the error's class: the first of context.Canceled, context.DeadlineExceeded and the domain sentinels
// (domain.ErrCancelled, domain.ErrNotFound, ..., domain.ErrUnavailable) that the error wraps, else a generic "error
// (details in the memd logs)". The message is never recorded, because memd's error chains can quote model output or
// gateway response bodies; the full error stays in the memd logs, which carry the trace id. The gRPC span records only
// the status code of a failed call, never its message, while the client still receives the full status; the contrib
// marks every non-OK code except CANCELLED as an error.
//
// Accepted gaps: claim and heartbeat statements are never traced, nor are the readiness probes' checks.
//
// # Testing
//
// The package tests and cmd/memd's wiring tests (cmd/memd/observability_test.go) use dd-trace-go's mocktracer:
// mocktracer.Start() installs an in-memory global tracer, [New] returns an enabled Telemetry over it without starting
// the real tracer, and FinishedSpans() exposes names, tags and parent ids to assert on. Tracer state is global, so
// those tests never run in parallel. cmd/memd's TestDatadogUnreachableAgentStillServes starts the real tracer against
// an unreachable agent (a closed local port) to show memd still serves. The SPEC says nothing about observability; the
// design above is the whole contract.
package observability
