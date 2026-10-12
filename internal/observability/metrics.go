package observability

import (
	"context"
	"log/slog"
	"memd/internal/domain"
	"net"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
)

// MetricNamespace prefixes every metric memd sends; the Metric* names below are without it.
const MetricNamespace = "memd."

// memd's metrics (sent as MetricNamespace + name). Tags are bounded: method, code, kind, outcome, status,
// slot, model, route, error class and ingest reason. Never a tenant, bank, document or operation id, query text or
// content.
const (
	// MetricGRPCRequests counts finished RPCs; tags method (the full method) and code (the status code).
	MetricGRPCRequests = "grpc.requests"
	// MetricGRPCLatency is the RPC duration in ms (distribution); tags method and code.
	MetricGRPCLatency = "grpc.latency"
	// MetricJobStarted counts job runs the worker started; tag kind.
	MetricJobStarted = "job.started"
	// MetricJobCompleted counts runs that completed; tag kind.
	MetricJobCompleted = "job.completed"
	// MetricJobRetried counts runs that failed and were rescheduled; tag kind.
	MetricJobRetried = "job.retried"
	// MetricJobFailed counts runs that failed for good; tag kind.
	MetricJobFailed = "job.failed"
	// MetricJobReleased counts runs a shutdown released for another worker; tag kind.
	MetricJobReleased = "job.released"
	// MetricJobLost counts runs the worker lost (cancel requested, reclaimed or not heartbeated); tag kind.
	MetricJobLost = "job.lost"
	// MetricJobDuration is a run's duration in ms, settle included (distribution); tags kind and outcome.
	MetricJobDuration = "job.duration"
	// MetricGatewayCalls counts model calls (each with all its retries); tags slot, model and route.
	MetricGatewayCalls = "gateway.calls"
	// MetricGatewayLatency is a model call's duration in ms, retries included (distribution); tags slot, model
	// and route.
	MetricGatewayLatency = "gateway.latency"
	// MetricGatewayErrors counts failed model calls; tags slot, model, route and error (the error class).
	MetricGatewayErrors = "gateway.errors"
	// MetricGatewayTokensIn counts prompt tokens the gateway reported; tags slot and model.
	MetricGatewayTokensIn = "gateway.tokens.input"
	// MetricGatewayTokensOut counts completion tokens the gateway reported; tags slot and model.
	MetricGatewayTokensOut = "gateway.tokens.output"
	// MetricIngestReceived counts the messages the queue consumer received.
	MetricIngestReceived = "ingest.received"
	// MetricIngestAcked counts messages acked after their retain was committed (or found unchanged, stale or
	// superseded).
	MetricIngestAcked = "ingest.acked"
	// MetricIngestNacked counts messages handed back for redelivery; tag reason.
	MetricIngestNacked = "ingest.nacked"
	// MetricIngestDeadLettered counts messages moved to the dead-letter destination; tag reason.
	MetricIngestDeadLettered = "ingest.dead_lettered"
	// MetricIngestDropped counts messages acked without a retain because their org is deleted; tag reason.
	MetricIngestDropped = "ingest.dropped"
	// MetricIngestBatchLatency is one consumer RetainBatch call's duration in ms (distribution); tag error (the
	// error class, "none" on success).
	MetricIngestBatchLatency = "ingest.batch.latency"
	// MetricQueueDepth is the number of pending or processing jobs (gauge); tags kind and status.
	MetricQueueDepth = "queue.depth"
	// MetricQueueOldestAge is how long the oldest due pending job has waited past its run time, in seconds
	// (gauge); tag kind.
	MetricQueueOldestAge = "queue.oldest_pending_age_seconds"
	// MetricPoolAcquired is the Postgres pool's connections in use (gauge).
	MetricPoolAcquired = "postgres.pool.acquired"
	// MetricPoolIdle is the pool's idle connections (gauge).
	MetricPoolIdle = "postgres.pool.idle"
	// MetricPoolTotal is the pool's open connections, constructing ones included (gauge).
	MetricPoolTotal = "postgres.pool.total"
	// MetricPoolMax is the pool's size cap (gauge).
	MetricPoolMax = "postgres.pool.max"
	// MetricPoolAcquires counts connection acquires since the last sample.
	MetricPoolAcquires = "postgres.pool.acquires"
	// MetricPoolEmptyAcquires counts acquires that had to wait for a free connection.
	MetricPoolEmptyAcquires = "postgres.pool.empty_acquires"
	// MetricPoolCanceledAcquires counts acquires cancelled by their context while waiting.
	MetricPoolCanceledAcquires = "postgres.pool.canceled_acquires"
	// MetricPoolEmptyAcquireWait counts the ms acquires spent waiting for a free connection.
	MetricPoolEmptyAcquireWait = "postgres.pool.empty_acquire_wait_ms"
)

// defaultStatsdPort is DogStatsD's UDP port when neither DD_DOGSTATSD_URL nor DD_DOGSTATSD_PORT says otherwise.
const defaultStatsdPort = "8125"

// newStatsClient returns the DogStatsD client, or nil (logged) when it can't be created. The address follows
// the library's conventions: DD_DOGSTATSD_URL (udp://host:port or unix:///path), else DD_AGENT_HOST with
// DD_DOGSTATSD_PORT (default 8125); with neither set, localhost, as for the tracer. The library adds env,
// service and version tags from DD_ENV, DD_SERVICE and DD_VERSION; service:memd is added when DD_SERVICE is
// unset. Sending is UDP (or a unix socket), buffered and fire-and-forget: an absent agent never blocks or
// fails a caller.
func newStatsClient(env func(string) string, log *slog.Logger) statsd.ClientInterface {
	addr := "" // the library reads DD_DOGSTATSD_URL, DD_AGENT_HOST and DD_DOGSTATSD_PORT itself
	if env(envStatsdURL) == "" && env(envAgentHost) == "" {
		port := env(envStatsdPort)
		if port == "" {
			port = defaultStatsdPort
		}
		addr = net.JoinHostPort("localhost", port)
	}
	opts := []statsd.Option{statsd.WithNamespace(MetricNamespace)}
	if env(envService) == "" {
		opts = append(opts, statsd.WithTags([]string{"service:" + DefaultService}))
	}
	c, err := statsd.New(addr, opts...)
	if err != nil {
		log.Warn("datadog: dogstatsd client not created; running without metrics", "err", err)
		return nil
	}
	return c
}

// count adds n to counter name; a send error (a full buffer) only drops the point.
func (t *Telemetry) count(name string, n int64, tags ...string) {
	_ = t.stats.Count(name, n, tags, 1)
}

// gauge sets gauge name.
func (t *Telemetry) gauge(name string, v float64, tags ...string) {
	_ = t.stats.Gauge(name, v, tags, 1)
}

// millis records d in ms on distribution name.
func (t *Telemetry) millis(name string, d time.Duration, tags ...string) {
	_ = t.stats.Distribution(name, float64(d)/float64(time.Millisecond), tags, 1)
}

// tag formats one name:value tag.
func tag(name, value string) string { return name + ":" + value }

// jobOutcomeMetrics maps a job outcome to its counter. The keys are the values of jobs.OutcomeCompleted,
// OutcomeRetry, OutcomeFailed, OutcomeReleased and OutcomeLost, written out so this package doesn't depend on
// the job runner; keep them in sync (cmd/memd's TestDatadogMetrics checks completed end to end).
var jobOutcomeMetrics = map[string]string{
	"completed": MetricJobCompleted,
	"retry":     MetricJobRetried,
	"failed":    MetricJobFailed,
	"released":  MetricJobReleased,
	"lost":      MetricJobLost,
}

// jobMetrics counts a job run's start and returns the func that records its end.
func (t *Telemetry) jobMetrics(kind domain.OperationKind) func(outcome string) {
	start := time.Now()
	k := tag("kind", string(kind))
	t.count(MetricJobStarted, 1, k)
	return func(outcome string) {
		if name, ok := jobOutcomeMetrics[outcome]; ok {
			t.count(name, 1, k)
		}
		t.millis(MetricJobDuration, time.Since(start), k, tag("outcome", outcome))
	}
}

// gatewayMetrics returns the func that records one gateway call's end.
func (t *Telemetry) gatewayMetrics(slot domain.ModelSlot, model, path string) func(error) {
	start := time.Now()
	tags := []string{tag("slot", string(slot)), tag("model", model), tag("route", path)}
	return func(err error) {
		t.count(MetricGatewayCalls, 1, tags...)
		t.millis(MetricGatewayLatency, time.Since(start), tags...)
		if err != nil {
			t.count(MetricGatewayErrors, 1, append(tags, tag("error", errorClassOf(err)))...)
		}
	}
}

// UsageHook returns the gateway's usage hook (assign it to gateway.Config.OnUsage): it counts the input and
// output tokens of every successful gateway HTTP call by slot and model. Without metrics it returns nil.
func (t *Telemetry) UsageHook() func(context.Context, domain.Usage) {
	if !t.metered() {
		return nil
	}
	return func(_ context.Context, u domain.Usage) {
		tags := []string{tag("slot", string(u.Slot)), tag("model", u.Model)}
		if u.InputTokens > 0 {
			t.count(MetricGatewayTokensIn, u.InputTokens, tags...)
		}
		if u.OutputTokens > 0 {
			t.count(MetricGatewayTokensOut, u.OutputTokens, tags...)
		}
	}
}
