# 2400: Optional Datadog observability (WP-24)

## Context
The user asked for Datadog observability across the service. Two constraints came with the request:
- "It can't be mandatory."
- "Don't use otel clients, go directly for the dd client."

The SPEC says nothing about observability.

## Decision
- **Library.** memd uses dd-trace-go v2 directly.
  - Versions: `github.com/DataDog/dd-trace-go/v2` v2.10.1, plus these contrib modules at the same version:
    - `contrib/google.golang.org/grpc/v2`
    - `contrib/net/http/v2`
    - `contrib/aws/aws-sdk-go-v2/v2`
    - `contrib/log/slog/v2`
  - Why v2.10.1: it is the newest release older than two weeks. v2.11.x is three days old and requires Go 1.26.
  - memd imports no OpenTelemetry SDK, bridge or `ddtrace/opentelemetry` package. The
    `go.opentelemetry.io/collector/*` and `otlp` modules in go.sum are transitive dependencies of the
    dd-trace-go core; memd does not import them.
- **Off by default.**
  - `MEMD_DATADOG_ENABLED` (flag `-datadog-enabled`, default false) turns on:
    - the tracer
    - all instrumentation
    - slog trace-id correlation
    - runtime metrics, unless `DD_RUNTIME_METRICS_ENABLED` is set
  - `MEMD_DATADOG_PROFILER` (default false) also starts the profiler, and only when Datadog is enabled. With
    Datadog off it is ignored rather than rejected, so you can toggle the profiler without editing two
    variables.
  - When disabled, `observability.Start` returns a nil `*Telemetry`. Every helper is nil-safe and returns nil,
    or its input unchanged, so wiring and behaviour are exactly as before. No tracer starts and no agent is
    contacted.
- **Standard DD_* variables.** The library reads these itself:
  - agent: `DD_AGENT_HOST`, `DD_TRACE_AGENT_URL`
  - unified service tagging: `DD_ENV`, `DD_VERSION`
  - `DD_TAGS`
  - sampling
  - DogStatsD

  `DD_SERVICE` defaults to `memd`. memd passes `WithService("memd")` only when `DD_SERVICE` is unset,
  because a code option would otherwise outrank the variable.
- **Never fails.** Failures in the tracer or profiler are logged and memd runs untraced.
  - If `tracer.Start` or `profiler.Start` returns an error, memd continues without tracing or profiling.
  - An unreachable agent only means dropped payloads.
  - The tracer's one-time feature probe at startup returns at once when the connection is refused. A
    black-holed address can delay startup by up to the tracer's 10 s HTTP timeout.
- **Vendor isolation.** Only `internal/observability` imports `github.com/DataDog`.
  - The worker, the gateway adapter and the S3 adapter expose vendor-free hooks, each nil by default:
    - `worker.RunnerConfig.TraceJob`
    - `worker.MaintenanceDeps.TraceTick`
    - `gateway.Config.TraceCall`
    - `s3.Config.APIOptions`
  - `cmd/memd/wire.go` fills them from the Telemetry.
  - depguard denies `github.com/DataDog` in `internal/engine/**`, `internal/domain/**` and `internal/port/**`
    (CCR WP-24-1).
- **What is traced.**

  | Span | Source | Resource | Tags |
  |---|---|---|---|
  | `grpc.server` | contrib | full method | `memd.tenant`, `memd.bank` |
  | `memd.job` | worker hook | job kind | `memd.operation_id`, `memd.tenant`, `memd.bank`, `memd.job.attempt`, `memd.job.outcome` |
  | `memd.maintenance.tick` | worker hook | `maintenance` | none |
  | `postgres.query` | memd's own pgx tracer | SQL text | `db.system`, `db.row_count` |
  | `memd.gateway.call` | gateway hook | route | `memd.model.slot`, `memd.model.name` |
  | `http.request` | contrib, under `memd.gateway.call` | | |
  | `S3.request` | contrib | `S3.<Operation>` | |

  - **gRPC.** The interceptor is installed as `grpc.UnaryInterceptor`, so it is the outermost one and calls
    the tenant check rejects are traced too. Health and reflection methods are not traced. The tags come
    from a second interceptor that runs after the tenant check.
  - **Worker jobs.** `memd.job.outcome` is one of completed, retry, failed, released or lost. A job span is
    an error unless the run completed. The handler and the terminal Complete/Fail run under the job span.
  - **Postgres.** Spans are recorded only under an existing span (request, job or tick).
  - Logs written with slog's `*Context` methods under a span carry `dd.trace_id` and `dd.span_id`. The
    worker logs its job and tick messages that way.
- **Postgres tracer.** The tracer is memd's own, not `contrib/jackc/pgx.v5`. The contrib traces every
  statement, connect and acquire, and with no parent each becomes its own root trace. The worker's claim
  polls (every 500 ms) and heartbeats would flood the trace stream. memd's ~40-line `pgx.QueryTracer` traces
  only statements whose context already carries a span. memd sends no pgx batches or CopyFrom, so
  QueryTracer covers every statement.
- **Never tagged.** Spans never record these:
  - request or response bodies
  - gRPC metadata
  - prompts, messages and tool arguments
  - model output
  - memory or document content
  - SQL bind parameters
  - HTTP headers

  Span errors on memd's own spans (job, tick, gateway call, Postgres) carry only the error class: the domain
  sentinel ("unavailable", "not found", ...), a context error, or a generic "details in the memd logs". The
  message is left out because memd's error chains can quote model output, gateway response bodies or key
  values; the logs keep the full error with the trace id. The gRPC span's error is the status message the
  client receives anyway. The contrib marks every non-OK code except CANCELLED as an error.

## Consequences
- With Datadog off, the cost is a nil check per job, tick and gateway call.
- With Datadog on, the agent is the operator's to run; the README has a compose snippet.
- **Accepted gaps:**
  - Claim and heartbeat statements are never traced.
  - The readiness probes' HTTP and Postgres checks are not traced.
  - The contrib's own S3 and HTTP span tags (bucket, URL path, status) are outside memd's control, but none
    carries content.
