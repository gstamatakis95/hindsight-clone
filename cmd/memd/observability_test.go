package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"memd/internal/jobs"
	"memd/internal/observability"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	pb "memd/gen/memory/v1"
)

// The Datadog tests swap the global tracer, so none of them runs in parallel. Each registers the tracer's
// stop before starting the stack, so the stack (worker included) is gone before the tracer stops.

// retainOne retains one document into bank and waits until its jobs (consolidation) have run.
func retainOne(t *testing.T, w *wiredStack, bank string) {
	t.Helper()
	mem := pb.NewMemoryServiceClient(w.conn)
	ret, err := mem.Retain(w.ctx(t), &pb.RetainRequest{BankId: bank, Items: []*pb.RetainItem{{
		Content: "Alice moved to Lisbon in 2021. Alice works as a nurse.", DocumentId: "d1",
	}}})
	if err != nil || ret.GetDocuments()[0].GetMemoriesCreated() == 0 {
		t.Fatalf("retain: %v %v", ret, err)
	}
	w.idle(t, bank)
}

// TestDatadogDisabledInstallsNothing runs RPCs and jobs through the stack wired without Datadog and checks
// that not one span was started, even with a tracer installed globally.
func TestDatadogDisabledInstallsNothing(t *testing.T) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	tel, stop := observability.Start(observability.Config{Profiler: true})
	t.Cleanup(stop)
	if tel != nil || tel.Enabled() {
		t.Fatalf("disabled Start returned an enabled Telemetry")
	}
	w := startWiredWith(t, tel)
	retainOne(t, w, "b")
	if n, o := len(mt.FinishedSpans()), len(mt.OpenSpans()); n+o != 0 {
		t.Fatalf("disabled: %d finished and %d open spans, want none", n, o)
	}
}

// TestDatadogSpansNest checks the spans an enabled stack produces: the gRPC span tagged with tenant and
// bank, the gateway calls under it with their HTTP attempts under them, and a worker job span with its own
// gateway calls under it.
func TestDatadogSpansNest(t *testing.T) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	w := startWiredWith(t, observability.New(slog.New(slog.NewTextHandler(io.Discard, nil))))
	retainOne(t, w, "b")
	// A job span finishes just after its row is settled, so idle() can return a moment before it does; wait
	// for the consolidate job's and a tick's spans.
	spans := waitSpans(t, mt, func(spans []*mocktracer.Span) bool {
		return findSpan(spans, isConsolidateJob) != nil &&
			findSpan(spans, func(s *mocktracer.Span) bool { return s.OperationName() == observability.SpanTick }) != nil
	})
	checkRPCSpans(t, spans)
	checkJobSpans(t, spans)
}

// isConsolidateJob matches a consolidate job span.
func isConsolidateJob(s *mocktracer.Span) bool {
	return s.OperationName() == observability.SpanJob && s.Tag(ext.ResourceName) == "consolidate"
}

// waitSpans polls the finished spans until done accepts them (or 10 s pass) and returns them.
func waitSpans(t *testing.T, mt mocktracer.Tracer, done func([]*mocktracer.Span) bool) []*mocktracer.Span {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		spans := mt.FinishedSpans()
		if done(spans) || time.Now().After(deadline) {
			return spans
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// checkRPCSpans checks the Retain RPC's span, its extract gateway call and that call's HTTP attempt.
func checkRPCSpans(t *testing.T, spans []*mocktracer.Span) {
	t.Helper()
	rpc := findSpan(spans, func(s *mocktracer.Span) bool {
		return s.OperationName() == "grpc.server" && s.Tag(ext.ResourceName) == pb.MemoryService_Retain_FullMethodName
	})
	if rpc == nil {
		t.Fatalf("no grpc.server span for Retain among %d spans", len(spans))
	}
	if rpc.Tag(observability.TagTenant) != "t" || rpc.Tag(observability.TagBank) != "b" {
		t.Fatalf("Retain span tags: %v", rpc.Tags())
	}
	call := findSpan(spans, func(s *mocktracer.Span) bool {
		return s.OperationName() == observability.SpanGatewayCall && s.ParentID() == rpc.SpanID() &&
			s.Tag(observability.TagModelSlot) == "extract"
	})
	if call == nil || call.Tag(observability.TagModelName) == "" || call.Tag(ext.ResourceName) != "/chat/completions" {
		t.Fatalf("no extract gateway call under the Retain span: %v", call)
	}
	if findSpan(spans, func(s *mocktracer.Span) bool {
		return s.OperationName() == "http.request" && s.ParentID() == call.SpanID()
	}) == nil {
		t.Fatal("no http.request span under the gateway call")
	}
}

// checkJobSpans checks the consolidate job's span, a gateway call under it and the maintenance tick span.
func checkJobSpans(t *testing.T, spans []*mocktracer.Span) {
	t.Helper()
	job := findSpan(spans, isConsolidateJob)
	if job == nil || job.Tag(observability.TagOutcome) != jobs.OutcomeCompleted ||
		job.Tag(observability.TagBank) != "b" || job.Tag(observability.TagTenant) != "t" ||
		job.Tag(observability.TagOperationID) == "" {
		t.Fatalf("consolidate job span: %v", job)
	}
	if findSpan(spans, func(s *mocktracer.Span) bool {
		return s.OperationName() == observability.SpanGatewayCall && s.ParentID() == job.SpanID()
	}) == nil {
		t.Fatal("no gateway call under the consolidate job span")
	}
	if findSpan(spans, func(s *mocktracer.Span) bool { return s.OperationName() == observability.SpanTick }) == nil {
		t.Fatal("no maintenance tick span")
	}
}

// TestDatadogSettings checks the Datadog switches: off by default, on from the environment, and a flag
// wins over the environment.
func TestDatadogSettings(t *testing.T) {
	env := baseEnv(t, baseFile)
	cfg, err := loadValidate([]string{"all"}, env)
	if err != nil || cfg.DatadogEnabled || cfg.DatadogProfiler || !cfg.DatadogMetrics {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	env[envDatadogEnabled], env[envDatadogProfiler] = "true", "true"
	if cfg, err = loadValidate([]string{"all"}, env); err != nil || !cfg.DatadogEnabled || !cfg.DatadogProfiler {
		t.Fatalf("from env: %+v %v", cfg, err)
	}
	if cfg, err = loadValidate([]string{"all", "-datadog-enabled=false"}, env); err != nil || cfg.DatadogEnabled {
		t.Fatalf("flag over env: %+v %v", cfg, err)
	}
	env[envDatadogEnabled] = "maybe"
	if _, err = loadValidate([]string{"all"}, env); err == nil {
		t.Fatal("MEMD_DATADOG_ENABLED=maybe was accepted")
	}
	env[envDatadogEnabled], env[envDatadogMetrics] = "true", "false"
	if cfg, err = loadValidate([]string{"all"}, env); err != nil || cfg.DatadogMetrics {
		t.Fatalf("metrics off: %+v %v", cfg, err)
	}
}

// TestLogFormat checks the log format and stream: text on stderr by default, JSON on stdout with Datadog
// enabled, an explicit format winning either way, and an unknown format rejected.
func TestLogFormat(t *testing.T) {
	cases := []struct {
		datadog, format string
		json, stdout    bool
	}{
		{"false", "", false, false},
		{"true", "", true, true},
		{"true", "text", false, true},
		{"false", "JSON", true, false},
	}
	for _, c := range cases {
		env := baseEnv(t, baseFile)
		env[envDatadogEnabled], env[envLogFormat] = c.datadog, c.format
		cfg, err := loadValidate([]string{"all"}, env)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		slog.New(logHandler(cfg, &stdout, &stderr)).Info("hello")
		out := &stderr
		if c.stdout {
			out = &stdout
		}
		line := out.String()
		if stdout.Len()+stderr.Len() != len(line) || !strings.Contains(line, "hello") {
			t.Fatalf("%+v: stdout %q, stderr %q", c, stdout.String(), stderr.String())
		}
		var rec map[string]any
		if isJSON := json.Unmarshal([]byte(line), &rec) == nil; isJSON != c.json || (c.json && rec["message"] != "hello") {
			t.Fatalf("%+v: line %q", c, line)
		}
	}
	env := baseEnv(t, baseFile)
	env[envLogFormat] = "xml"
	if _, err := loadValidate([]string{"all"}, env); err == nil {
		t.Fatal("MEMD_LOG_FORMAT=xml was accepted")
	}
}

// statsPoint is one metric sample a fakeStats received.
type statsPoint struct {
	name string
	tags []string
}

// fakeStats records the metrics sent to it; calling a method it does not override panics (the embedded
// interface is nil).
type fakeStats struct {
	statsd.ClientInterface
	mu     sync.Mutex
	points []statsPoint
}

// add records one sample.
func (f *fakeStats) add(name string, tags []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.points = append(f.points, statsPoint{name: name, tags: slices.Clone(tags)})
	return nil
}

// Count records a count.
func (f *fakeStats) Count(name string, _ int64, tags []string, _ float64) error {
	return f.add(name, tags)
}

// Gauge records a gauge.
func (f *fakeStats) Gauge(name string, _ float64, tags []string, _ float64) error {
	return f.add(name, tags)
}

// Distribution records a distribution sample.
func (f *fakeStats) Distribution(name string, _ float64, tags []string, _ float64) error {
	return f.add(name, tags)
}

// has reports whether a sample of name carried every one of tags.
func (f *fakeStats) has(name string, tags ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.points {
		if p.name == name && !slices.ContainsFunc(tags, func(tag string) bool { return !slices.Contains(p.tags, tag) }) {
			return true
		}
	}
	return false
}

// TestDatadogMetrics runs a retain through the stack wired with metrics only (no tracer) and checks that the
// RPC, its gateway calls and token usage, and the consolidate job each emit their metrics with bounded tags.
func TestDatadogMetrics(t *testing.T) {
	stats := &fakeStats{}
	w := startWiredWith(t, observability.NewMetrics(slog.New(slog.NewTextHandler(io.Discard, nil)), stats))
	retainOne(t, w, "b")
	kind := "kind:consolidate"
	deadline := time.Now().Add(10 * time.Second) // the job's metrics follow its settle by a moment
	for !stats.has(observability.MetricJobDuration, kind, "outcome:completed") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	retain := []string{"method:" + pb.MemoryService_Retain_FullMethodName, "code:OK"}
	extract := []string{"slot:extract", "route:/chat/completions"}
	for _, want := range []struct {
		name string
		tags []string
	}{
		{observability.MetricGRPCRequests, retain},
		{observability.MetricGRPCLatency, retain},
		{observability.MetricGatewayCalls, extract},
		{observability.MetricGatewayLatency, extract},
		{observability.MetricGatewayTokensIn, []string{"slot:extract"}},
		{observability.MetricJobStarted, []string{kind}},
		{observability.MetricJobCompleted, []string{kind}},
		{observability.MetricJobDuration, []string{kind, "outcome:completed"}},
	} {
		if !stats.has(want.name, want.tags...) {
			t.Errorf("no %s with %v", want.name, want.tags)
		}
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	for _, p := range stats.points {
		for _, tag := range p.tags {
			if name, _, _ := strings.Cut(tag, ":"); !slices.Contains(
				[]string{"method", "code", "kind", "outcome", "slot", "model", "route", "error"}, name) {
				t.Errorf("%s carries tag %q", p.name, tag)
			}
		}
	}
}

// findSpan returns the first span matching ok, or nil.
func findSpan(spans []*mocktracer.Span, ok func(*mocktracer.Span) bool) *mocktracer.Span {
	for _, s := range spans {
		if ok(s) {
			return s
		}
	}
	return nil
}

// TestDatadogUnreachableAgentStillServes starts the real tracer and profiler against an agent address
// nothing listens on and checks the stack still serves RPCs and runs jobs.
func TestDatadogUnreachableAgentStillServes(t *testing.T) {
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close() // connection refused from now on
	t.Setenv("DD_TRACE_AGENT_URL", "http://"+addr)
	t.Setenv("DD_DOGSTATSD_URL", "udp://"+addr)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	tel, stop := observability.Start(observability.Config{
		Enabled: true, Profiler: true, Metrics: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(stop)
	if !tel.Enabled() {
		t.Fatal("enabled Start returned a disabled Telemetry")
	}
	w := startWiredWith(t, tel)
	retainOne(t, w, "b")
	rec, err := pb.NewMemoryServiceClient(w.conn).Recall(w.ctx(t), &pb.RecallRequest{BankId: "b", Query: "Lisbon"})
	if err != nil || len(rec.GetResults()) == 0 {
		t.Fatalf("recall with an unreachable agent: %v %v", rec, err)
	}
}
