package observability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/blob/s3"
	"memd/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
)

// These tests swap the global tracer, so none of them runs in parallel.

// TestDisabledStartsAndInstallsNothing checks that a disabled Start contacts no agent and that every helper
// of the nil Telemetry installs nothing.
func TestDisabledStartsAndInstallsNothing(t *testing.T) {
	var hits atomic.Int64
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer agent.Close()
	t.Setenv("DD_TRACE_AGENT_URL", agent.URL)
	tel, stop := Start(Config{Profiler: true})
	if tel != nil || tel.Enabled() {
		t.Fatal("disabled Start returned an enabled Telemetry")
	}
	h := slog.NewTextHandler(&bytes.Buffer{}, nil)
	hc := &http.Client{}
	pcfg, err := pgxpool.ParseConfig("postgres://u@localhost/db")
	if err != nil {
		t.Fatal(err)
	}
	tel.TracePool(pcfg)
	switch {
	case tel.GRPCServerOptions(nil) != nil, tel.JobHook() != nil, tel.TickHook() != nil, tel.GatewayHook() != nil,
		tel.S3APIOptions() != nil:
		t.Fatal("disabled Telemetry returned instrumentation")
	case tel.HTTPClient(hc) != hc || tel.HTTPClient(nil) != nil || tel.LogHandler(h) != h:
		t.Fatal("disabled Telemetry wrapped its input")
	case pcfg.ConnConfig.Tracer != nil:
		t.Fatal("disabled Telemetry installed a pgx tracer")
	}
	stop()
	if n := hits.Load(); n != 0 {
		t.Fatalf("disabled Start contacted the agent %d times", n)
	}
}

// TestPgxTracerOnlyUnderParent checks that a statement is traced under an existing span, with the SQL as
// resource and only the error class, and that a statement outside any span starts nothing.
func TestPgxTracerOnlyUnderParent(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tr := pgxTracer{}
	ctx := context.Background()
	if got := tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"}); got != ctx {
		t.Fatal("statement without a parent span got a new context")
	}
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if n := len(mt.FinishedSpans()) + len(mt.OpenSpans()); n != 0 {
		t.Fatalf("statement without a parent span started %d spans", n)
	}
	parent, pctx := tracer.StartSpanFromContext(ctx, "parent")
	qctx := tr.TraceQueryStart(pctx, nil, pgx.TraceQueryStartData{SQL: "UPDATE t SET x = $1"})
	tr.TraceQueryEnd(qctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("UPDATE 3")})
	ectx := tr.TraceQueryStart(pctx, nil, pgx.TraceQueryStartData{SQL: "INSERT"})
	tr.TraceQueryEnd(ectx, nil, pgx.TraceQueryEndData{Err: errors.New("duplicate key (id)=(alice's secret)")})
	parent.Finish()
	spans := mt.FinishedSpans()
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 2 statements and the parent", len(spans))
	}
	q, e := spans[0], spans[1]
	if q.OperationName() != SpanPostgresQuery || q.ParentID() != parent.Context().SpanID() ||
		q.Tag(ext.ResourceName) != "UPDATE t SET x = $1" || q.Tag(ext.DBSystem) != ext.DBSystemPostgreSQL ||
		fmt.Sprint(q.Tag("db.row_count")) != "3" {
		t.Fatalf("statement span: %v", q)
	}
	if msg := fmt.Sprint(e.Tag(ext.ErrorMsg)); strings.Contains(msg, "secret") || msg != errUnclassified.Error() {
		t.Fatalf("statement error message %q leaks the error or lacks the class", msg)
	}
}

// TestGatewayAndJobSpansCarryOnlyIdsAndErrorClass checks the gateway and job spans' tags and that a failed
// call records the error class, never the message.
func TestGatewayAndJobSpansCarryOnlyIdsAndErrorClass(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tel := New(nil)
	ctx, done := tel.GatewayHook()(context.Background(), domain.ModelSlotExtract, "m1", "/chat/completions")
	if _, ok := tracer.SpanFromContext(ctx); !ok {
		t.Fatal("gateway hook returned a context without its span")
	}
	done(fmt.Errorf("%w: gateway said: the prompt was 'my secret diary'", domain.ErrUnavailable))
	_, tick := tel.TickHook()(context.Background())
	tick(nil)
	spans := mt.FinishedSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	g := spans[0]
	if g.OperationName() != SpanGatewayCall || g.Tag(TagModelSlot) != "extract" || g.Tag(TagModelName) != "m1" ||
		g.Tag(ext.ResourceName) != "/chat/completions" || g.Tag(ext.ErrorMsg) != domain.ErrUnavailable.Error() {
		t.Fatalf("gateway span: %v", g)
	}
	if spans[1].OperationName() != SpanTick || spans[1].Tag(ext.ErrorMsg) != nil {
		t.Fatalf("tick span: %v", spans[1])
	}
}

// TestGRPCSpanErrorIsCodeOnly checks that a failed RPC's span records only the status code while the
// client still receives the full status message.
func TestGRPCSpanErrorIsCodeOnly(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	icpt := codeOnlyErrors(grpctrace.UnaryServerInterceptor())
	info := &grpc.UnaryServerInfo{FullMethod: "/memory.v1.MemoryService/Recall"}
	_, err := icpt(context.Background(), nil, info, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Unavailable, "gateway said: the prompt was 'my secret diary'")
	})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("client error = %v, want the full status", err)
	}
	resp, err := icpt(context.Background(), nil, info, func(context.Context, any) (any, error) { return "ok", nil })
	if err != nil || resp != "ok" {
		t.Fatalf("successful call = %v, %v", resp, err)
	}
	spans := mt.FinishedSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if msg := fmt.Sprint(spans[0].Tag(ext.ErrorMsg)); strings.Contains(msg, "secret") {
		t.Fatalf("span error message %q leaks the status message", msg)
	}
	if spans[0].Tag(ext.ResourceName) != info.FullMethod || spans[1].Tag(ext.ErrorMsg) != nil {
		t.Fatalf("spans: %v", spans)
	}
}

// TestS3Spans checks that the S3 middleware traces a blob write under the caller's span.
func TestS3Spans(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	blob, err := s3.New(context.Background(), s3.Config{
		Endpoint: srv.URL, Bucket: "memd", AccessKeyID: "a", SecretAccessKey: "s", UsePathStyle: true,
		APIOptions: New(nil).S3APIOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	if err := blob.Put(ctx, "t/b/doc", strings.NewReader("x"), 1, "text/plain"); err != nil {
		t.Fatal(err)
	}
	parent.Finish()
	for _, s := range mt.FinishedSpans() {
		if s.Tag(ext.ResourceName) == "S3.PutObject" && s.ParentID() == parent.Context().SpanID() {
			return
		}
	}
	t.Fatalf("no S3.PutObject span under the parent: %v", mt.FinishedSpans())
}

// TestLogHandlerAddsTraceIDs checks log correlation: a record logged with a span in its context carries
// dd.trace_id and dd.span_id; one without does not.
func TestLogHandlerAddsTraceIDs(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	var buf bytes.Buffer
	log := slog.New(New(nil).LogHandler(slog.NewTextHandler(&buf, nil)))
	span, ctx := tracer.StartSpanFromContext(context.Background(), "op")
	log.InfoContext(ctx, "with span")
	span.Finish()
	log.Info("without span")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], ext.LogKeyTraceID+"=") ||
		!strings.Contains(lines[0], ext.LogKeySpanID+"=") || strings.Contains(lines[1], ext.LogKeyTraceID) {
		t.Fatalf("log lines:\n%s", buf.String())
	}
}
