package observability

import (
	"log/slog"
	"os"

	"github.com/DataDog/datadog-go/v5/statsd"
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
	// TagOrg is the org id (gRPC and job spans).
	TagOrg = "memd.org"
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

// Environment variables Start reads itself; every other DD_* variable is read by dd-trace-go or datadog-go.
const (
	envService        = "DD_SERVICE"
	envEnv            = "DD_ENV"
	envVersion        = "DD_VERSION"
	envRuntimeMetrics = "DD_RUNTIME_METRICS_ENABLED"
	envAgentHost      = "DD_AGENT_HOST"
	envStatsdURL      = "DD_DOGSTATSD_URL"
	envStatsdPort     = "DD_DOGSTATSD_PORT"
)

// Config switches Datadog on. The zero value is off.
type Config struct {
	// Enabled starts the tracer and installs the instrumentation (MEMD_DATADOG_ENABLED).
	Enabled bool
	// Profiler also starts the continuous profiler (MEMD_DATADOG_PROFILER); ignored unless Enabled.
	Profiler bool
	// Metrics also sends memd's own metrics to DogStatsD (MEMD_DATADOG_METRICS, which cmd/memd defaults to
	// true); ignored unless Enabled.
	Metrics bool
	// Logger receives memd's and the tracer's own messages; nil = slog.Default().
	Logger *slog.Logger
	// Env looks up environment variables (DD_SERVICE, DD_ENV, DD_VERSION, DD_RUNTIME_METRICS_ENABLED and the
	// DogStatsD address); nil = os.Getenv.
	Env func(string) string
}

// Telemetry hands out the instrumentation for each layer. A nil *Telemetry is the disabled state: every
// method is safe on it and returns "install nothing" (nil options, nil hooks, the input unchanged), so the
// composition root calls the same methods either way. An enabled one traces, sends metrics, or both: a
// tracer that failed to start leaves metrics on, and a metrics client that failed leaves tracing on.
type Telemetry struct {
	// log receives instrumentation warnings.
	log *slog.Logger
	// tracing is set when the tracer runs; the tracing helpers install nothing without it.
	tracing bool
	// logAttrs are the dd.service, dd.env and dd.version attributes LogHandler adds when tracing.
	logAttrs []slog.Attr
	// stats sends memd's metrics; nil = metrics off.
	stats statsd.ClientInterface
}

// Start starts Datadog per cfg and returns the Telemetry to wire with and a stop func that closes the
// metrics client (flushing it) and flushes and stops the tracer and profiler (call it once, at exit).
// Disabled, it starts nothing, contacts nothing and returns (nil, no-op).
//
// Enabled, it starts the dd-trace-go tracer (service DefaultService unless DD_SERVICE is set, runtime
// metrics on unless DD_RUNTIME_METRICS_ENABLED is set; the agent address, DD_ENV, DD_VERSION and sampling
// come from the standard DD_* variables), with cfg.Profiler the profiler, and with cfg.Metrics the DogStatsD
// client (see newStatsClient). None of them ever fails startup: a start error is logged and memd runs without
// that part; an unreachable agent only costs dropped payloads.
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
	t := &Telemetry{log: log, logAttrs: logAttrs(env)}
	stopTracer := t.startTracer(cfg.Profiler, env)
	if cfg.Metrics {
		t.stats = newStatsClient(env, log)
	}
	if !t.tracing && t.stats == nil {
		return nil, func() {}
	}
	log.Info("datadog enabled", "tracing", t.tracing, "profiler", t.tracing && cfg.Profiler, "metrics", t.stats != nil)
	return t, func() {
		if t.stats != nil {
			_ = t.stats.Close()
		}
		stopTracer()
	}
}

// startTracer starts the tracer and, with prof, the profiler, and returns the func that stops both. A tracer
// start error is logged and leaves t.tracing unset (and the profiler off).
func (t *Telemetry) startTracer(prof bool, env func(string) string) func() {
	opts := []tracer.StartOption{tracer.WithLogger(ddLogger{log: t.log})}
	if env(envService) == "" {
		opts = append(opts, tracer.WithService(DefaultService))
	}
	if env(envRuntimeMetrics) == "" {
		opts = append(opts, tracer.WithRuntimeMetrics())
	}
	if err := tracer.Start(opts...); err != nil {
		t.log.Warn("datadog: tracer did not start; running without tracing", "err", err)
		return func() {}
	}
	t.tracing = true
	stopProfiler := func() {}
	if prof {
		stopProfiler = startProfiler(env, t.log)
	}
	return func() {
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

// New returns a tracing Telemetry over whatever global tracer is running, without starting one and without
// metrics: tests use it after mocktracer.Start. log nil = slog.Default().
func New(log *slog.Logger) *Telemetry {
	if log == nil {
		log = slog.Default()
	}
	return &Telemetry{log: log, tracing: true, logAttrs: logAttrs(os.Getenv)}
}

// NewMetrics returns a metrics-only Telemetry (no tracing) that sends through c: tests use it with a fake
// client. log nil = slog.Default().
func NewMetrics(log *slog.Logger, c statsd.ClientInterface) *Telemetry {
	if log == nil {
		log = slog.Default()
	}
	return &Telemetry{log: log, stats: c}
}

// traces reports whether t's tracing helpers install anything.
func (t *Telemetry) traces() bool { return t != nil && t.tracing }

// metered reports whether t sends metrics.
func (t *Telemetry) metered() bool { return t != nil && t.stats != nil }
