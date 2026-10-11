package observability

import (
	"log/slog"
	"os"
	"strings"

	slogtrace "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/profiler"
)

// DefaultService is the Datadog service name memd reports when DD_SERVICE is unset.
const DefaultService = "memd"

// Operation names of the spans memd creates itself. The gRPC server ("grpc.server"), gateway HTTP
// ("http.request") and S3 ("S3.request") spans come from the dd-trace-go contrib packages.
const (
	// SpanJob is one run of one worker job; resource = the job kind.
	SpanJob = "memd.job"
	// SpanTick is one maintenance tick; resource = "maintenance".
	SpanTick = "memd.maintenance.tick"
	// SpanGatewayCall is one AI gateway call with its retries; resource = the route (/chat/completions,
	// /embeddings, /rerank).
	SpanGatewayCall = "memd.gateway.call"
	// SpanPostgresQuery is one Postgres statement; resource = the SQL text (bind parameters are never
	// recorded).
	SpanPostgresQuery = "postgres.query"
)

// Tags memd sets on its spans. Ids and names only: never request bodies, prompts, model output or memory
// content.
const (
	// TagTenant is the tenant id (gRPC and job spans).
	TagTenant = "memd.tenant"
	// TagBank is the bank id (gRPC and job spans).
	TagBank = "memd.bank"
	// TagOperationID is the job's operation id.
	TagOperationID = "memd.operation_id"
	// TagAttempt is the job's attempt number (0 on the first run).
	TagAttempt = "memd.job.attempt"
	// TagOutcome is how the job run ended: completed, retry, failed, released or lost.
	TagOutcome = "memd.job.outcome"
	// TagModelSlot is the model slot of a gateway call (extract, reflect, embed, rerank, ...).
	TagModelSlot = "memd.model.slot"
	// TagModelName is the gateway model name of a call.
	TagModelName = "memd.model.name"
)

// Environment variables Start reads itself; every other DD_* variable is read by dd-trace-go.
const (
	envService        = "DD_SERVICE"
	envRuntimeMetrics = "DD_RUNTIME_METRICS_ENABLED"
)

// Config switches Datadog on. The zero value is off.
type Config struct {
	// Enabled starts the tracer and installs the instrumentation (MEMD_DATADOG_ENABLED).
	Enabled bool
	// Profiler also starts the continuous profiler (MEMD_DATADOG_PROFILER); ignored unless Enabled.
	Profiler bool
	// Logger receives memd's and the tracer's own messages; nil = slog.Default().
	Logger *slog.Logger
	// Env looks up environment variables (DD_SERVICE, DD_RUNTIME_METRICS_ENABLED); nil = os.Getenv.
	Env func(string) string
}

// Telemetry hands out the instrumentation for each layer. A nil *Telemetry is the disabled state: every
// method is safe on it and returns "install nothing" (nil options, nil hooks, the input unchanged), so the
// composition root calls the same methods either way.
type Telemetry struct {
	// log receives instrumentation warnings.
	log *slog.Logger
}

// Start starts Datadog per cfg and returns the Telemetry to wire with and a stop func that flushes and
// stops the tracer and profiler (call it once, at exit). Disabled, it starts nothing, contacts nothing
// and returns (nil, no-op).
//
// Enabled, it starts the dd-trace-go tracer (service DefaultService unless DD_SERVICE is set, runtime
// metrics on unless DD_RUNTIME_METRICS_ENABLED is set; the agent address, DD_ENV, DD_VERSION and sampling
// come from the standard DD_* variables) and, with cfg.Profiler, the profiler. Neither ever fails startup:
// a start error is logged and memd runs untraced; an unreachable agent only costs dropped payloads.
func Start(cfg Config) (*Telemetry, func()) {
	if !cfg.Enabled {
		return nil, func() {}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	env := cfg.Env
	if env == nil {
		env = os.Getenv
	}
	opts := []tracer.StartOption{tracer.WithLogger(ddLogger{log: log})}
	if env(envService) == "" {
		opts = append(opts, tracer.WithService(DefaultService))
	}
	if env(envRuntimeMetrics) == "" {
		opts = append(opts, tracer.WithRuntimeMetrics())
	}
	if err := tracer.Start(opts...); err != nil {
		log.Warn("datadog: tracer did not start; running without tracing", "err", err)
		return nil, func() {}
	}
	stopProfiler := func() {}
	if cfg.Profiler {
		stopProfiler = startProfiler(env, log)
	}
	log.Info("datadog tracing enabled", "profiler", cfg.Profiler)
	return New(log), func() {
		stopProfiler()
		tracer.Stop()
	}
}

// startProfiler starts the continuous profiler (CPU and heap, the library defaults) and returns its stop
// func; a start error is logged and yields a no-op.
func startProfiler(env func(string) string, log *slog.Logger) func() {
	var opts []profiler.Option
	if env(envService) == "" {
		opts = append(opts, profiler.WithService(DefaultService))
	}
	if err := profiler.Start(opts...); err != nil {
		log.Warn("datadog: profiler did not start; running without profiling", "err", err)
		return func() {}
	}
	return profiler.Stop
}

// New returns an enabled Telemetry over whatever global tracer is running, without starting one: Start
// uses it after tracer.Start, and tests use it after mocktracer.Start. log nil = slog.Default().
func New(log *slog.Logger) *Telemetry {
	if log == nil {
		log = slog.Default()
	}
	return &Telemetry{log: log}
}

// Enabled reports whether t installs instrumentation (false for nil).
func (t *Telemetry) Enabled() bool { return t != nil }

// LogHandler adds dd.trace_id and dd.span_id to every record logged with a context that carries a span
// (slog's *Context methods), so logs and traces correlate in Datadog. Disabled, it returns h unchanged.
func (t *Telemetry) LogHandler(h slog.Handler) slog.Handler {
	if t == nil {
		return h
	}
	return slogtrace.WrapHandler(h)
}

// ddLogger routes the tracer's and profiler's own messages into slog.
type ddLogger struct {
	// log receives the messages.
	log *slog.Logger
}

var _ tracer.Logger = ddLogger{}

// Log writes msg at warn when the tracer marks it as an error, else at info.
func (l ddLogger) Log(msg string) {
	msg = strings.TrimSpace(msg)
	if strings.Contains(msg, "ERROR:") || strings.Contains(msg, "WARN:") {
		l.log.Warn(msg, "component", "datadog")
		return
	}
	l.log.Info(msg, "component", "datadog")
}
