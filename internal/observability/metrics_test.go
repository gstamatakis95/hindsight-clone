package observability

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
)

// point is one metric sample a fakeStats received.
type point struct {
	name  string
	value float64
	tags  []string
}

// fakeStats records the counts, gauges and distributions sent to it. Calling any other method panics (the
// embedded interface is nil).
type fakeStats struct {
	statsd.ClientInterface
	mu     sync.Mutex
	points []point
}

// add records one sample.
func (f *fakeStats) add(name string, v float64, tags []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.points = append(f.points, point{name: name, value: v, tags: slices.Clone(tags)})
	return nil
}

// Count records a count.
func (f *fakeStats) Count(name string, v int64, tags []string, _ float64) error {
	return f.add(name, float64(v), tags)
}

// Gauge records a gauge.
func (f *fakeStats) Gauge(name string, v float64, tags []string, _ float64) error {
	return f.add(name, v, tags)
}

// Distribution records a distribution sample.
func (f *fakeStats) Distribution(name string, v float64, tags []string, _ float64) error {
	return f.add(name, v, tags)
}

// find returns the samples of name that carry every one of tags.
func (f *fakeStats) find(name string, tags ...string) []point {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []point
	for _, p := range f.points {
		if p.name == name && !slices.ContainsFunc(tags, func(tag string) bool { return !slices.Contains(p.tags, tag) }) {
			out = append(out, p)
		}
	}
	return out
}

// allTags returns every tag sent so far.
func (f *fakeStats) allTags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.points {
		out = append(out, p.tags...)
	}
	return out
}

// udpListener is a local DogStatsD stand-in.
type udpListener struct {
	conn net.PacketConn
}

// listenUDP listens on a free local UDP port and points DD_DOGSTATSD_URL at it.
func listenUDP(t *testing.T) udpListener {
	t.Helper()
	conn, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Setenv("DD_DOGSTATSD_URL", "udp://"+conn.LocalAddr().String())
	return udpListener{conn: conn}
}

// read returns the datagrams that arrive within wait, concatenated.
func (l udpListener) read(wait time.Duration) string {
	var out strings.Builder
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(wait)
	for {
		_ = l.conn.SetReadDeadline(deadline)
		n, _, err := l.conn.ReadFrom(buf)
		if err != nil {
			return out.String()
		}
		out.Write(buf[:n])
	}
}

// TestStatsClientSendsToDogStatsD checks the real client: the address from DD_DOGSTATSD_URL, the memd.
// namespace and the unified service tags (service:memd when DD_SERVICE is unset, env from DD_ENV).
func TestStatsClientSendsToDogStatsD(t *testing.T) {
	agent := listenUDP(t)
	t.Setenv("DD_SERVICE", "")
	t.Setenv("DD_ENV", "staging")
	c := newStatsClient(os.Getenv, nil)
	if c == nil {
		t.Fatal("no client")
	}
	NewMetrics(nil, c).count(MetricGRPCRequests, 1, "code:OK")
	if err := c.Close(); err != nil { // flushes
		t.Fatal(err)
	}
	got := agent.read(2 * time.Second)
	wants := []string{MetricNamespace + MetricGRPCRequests + ":1|c", "code:OK", "env:staging", "service:memd"}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("datagrams %q lack %q", got, want)
		}
	}
}

// TestGRPCMetricsAndSpans runs the composed outer interceptor (metrics around tracing) as GRPCServerOptions
// builds it: a failed call is counted and timed with its method and code, still gets its span, and a health
// check is neither measured nor traced.
func TestGRPCMetricsAndSpans(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	stats := &fakeStats{}
	tel := &Telemetry{log: New(nil).log, tracing: true, stats: stats}
	untraced := grpctrace.WithUntracedMethods(untracedMethods...)
	icpt := chain([]grpc.UnaryServerInterceptor{
		tel.metricsInterceptor(), codeOnlyErrors(grpctrace.UnaryServerInterceptor(untraced)),
	})
	recall := &grpc.UnaryServerInfo{FullMethod: "/memory.v1.MemoryService/Recall"}
	_, err := icpt(context.Background(), nil, recall, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Unavailable, "gateway down")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("error = %v", err)
	}
	health := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	if _, err := icpt(context.Background(), nil, health, func(context.Context, any) (any, error) {
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	tags := []string{"method:" + recall.FullMethod, "code:Unavailable"}
	if len(stats.find(MetricGRPCRequests, tags...)) != 1 || len(stats.find(MetricGRPCLatency, tags...)) != 1 {
		t.Fatalf("metrics: %+v", stats.points)
	}
	if len(stats.points) != 2 {
		t.Fatalf("the health check was measured: %+v", stats.points)
	}
	spans := mt.FinishedSpans()
	if len(spans) != 1 || spans[0].Tag(ext.ResourceName) != recall.FullMethod {
		t.Fatalf("spans: %v", spans)
	}
}

// TestJobAndGatewayMetricsCarryNoIDs checks that job and gateway metrics are tagged with the kind, outcome,
// slot, model, route and error class only (never the tenant, bank or operation id), and that a metrics-only
// Telemetry starts no span and installs no tracing.
func TestJobAndGatewayMetricsCarryNoIDs(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	stats := &fakeStats{}
	tel := NewMetrics(nil, stats)
	j := store.Job{ID: "op-1", Kind: domain.OperationKindRetainDocument, Bank: domain.NewBankRef("acme", "org", "b-1")}
	_, end := tel.JobHook()(context.Background(), j)
	end("retry", errors.New("boom"))
	_, done := tel.GatewayHook()(context.Background(), domain.ModelSlotExtract, "m1", "/chat/completions")
	done(fmt.Errorf("%w: gateway said: my secret prompt", domain.ErrUnavailable))
	tel.UsageHook()(context.Background(), domain.Usage{Slot: domain.ModelSlotExtract, Model: "m1", InputTokens: 7})

	kind := "kind:retain_document"
	gw := []string{"slot:extract", "model:m1", "route:/chat/completions"}
	for name, tags := range map[string][]string{
		MetricJobStarted: {kind}, MetricJobRetried: {kind}, MetricJobDuration: {kind, "outcome:retry"},
		MetricGatewayCalls: gw, MetricGatewayLatency: gw, MetricGatewayErrors: append(gw, "error:unavailable"),
		MetricGatewayTokensIn: {"slot:extract", "model:m1"},
	} {
		if len(stats.find(name, tags...)) != 1 {
			t.Fatalf("no %s with %v: %+v", name, tags, stats.points)
		}
	}
	for _, tag := range stats.allTags() {
		if strings.Contains(tag, "acme") || strings.Contains(tag, "b-1") || strings.Contains(tag, "op-1") ||
			strings.Contains(tag, "secret") {
			t.Fatalf("tag %q leaks an id or content", tag)
		}
	}
	if len(stats.find(MetricGatewayTokensOut)) != 0 {
		t.Fatal("zero output tokens were sent")
	}
	if n := len(mt.FinishedSpans()) + len(mt.OpenSpans()); n != 0 || tel.TickHook() != nil || tel.S3APIOptions() != nil {
		t.Fatalf("metrics-only Telemetry traced: %d spans", n)
	}
}

// fakeQueue returns first on its first call and nothing afterwards (a drained queue).
type fakeQueue struct {
	mu    sync.Mutex
	calls int
	first []store.QueueStat
}

// QueueStats returns the queue's depth.
func (q *fakeQueue) QueueStats(context.Context) ([]store.QueueStat, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls++
	if q.calls == 1 {
		return q.first, nil
	}
	return nil, nil
}

// called returns how many times QueueStats ran.
func (q *fakeQueue) called() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls
}

// TestSamplerSendsGauges runs the sampler over a fake queue and an unconnected pool: the first tick sends the
// depth by kind and status, the oldest pending age and the pool gauges; once the queue drains, the same series
// read 0 instead of going silent.
func TestSamplerSendsGauges(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://u@127.0.0.1:1/db?pool_max_conns=7")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := &fakeQueue{first: []store.QueueStat{
		{
			Kind: domain.OperationKindRetainDocument, Status: domain.OperationStatusPending, Jobs: 3,
			OldestDue: 90 * time.Second,
		},
		{Kind: domain.OperationKindConsolidate, Status: domain.OperationStatusProcessing, Jobs: 1},
	}}
	stats := &fakeStats{}
	stop := NewMetrics(nil, stats).StartSampler(t.Context(), 5*time.Millisecond, Sources{Queue: q, Pool: pool.Stat})
	for deadline := time.Now().Add(5 * time.Second); q.called() < 3 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	for _, c := range []struct {
		name        string
		tags        []string
		first, last float64
	}{
		{MetricQueueDepth, []string{"kind:retain_document", "status:pending"}, 3, 0},
		{MetricQueueDepth, []string{"kind:consolidate", "status:processing"}, 1, 0},
		{MetricQueueOldestAge, []string{"kind:retain_document"}, 90, 0},
		{MetricPoolMax, nil, 7, 7},
		{MetricPoolAcquired, nil, 0, 0},
		{MetricPoolEmptyAcquires, nil, 0, 0},
	} {
		var v []float64
		for _, p := range stats.find(c.name, c.tags...) {
			v = append(v, p.value)
		}
		if len(v) < 2 || v[0] != c.first || v[len(v)-1] != c.last {
			t.Errorf("%s %v samples = %v, want %v first and %v last", c.name, c.tags, v, c.first, c.last)
		}
	}
}
