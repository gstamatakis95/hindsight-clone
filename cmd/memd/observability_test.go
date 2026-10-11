package main

import (
	"io"
	"log/slog"
	"memd/internal/jobs"
	"memd/internal/observability"
	"net"
	"testing"
	"time"

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
	if err != nil || ret.GetDocuments()[0].GetUnitsCreated() == 0 {
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
	env := baseEnv()
	cfg, err := loadValidate([]string{"all"}, env)
	if err != nil || cfg.DatadogEnabled || cfg.DatadogProfiler {
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
		Enabled: true, Profiler: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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
