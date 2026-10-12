package observability

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"net/http"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobHook returns the worker's job hook (assign it to jobs.Config.TraceJob). While tracing: one SpanJob span
// per run, resource = the job kind, tagged with the operation id, tenant, bank, attempt and outcome, and
// marked as an error when the run did not complete. With metrics: MetricJobStarted, one outcome counter
// (MetricJobCompleted, ...) and MetricJobDuration, tagged with the kind (and the outcome) only. Disabled, it
// returns nil.
func (t *Telemetry) JobHook() func(context.Context, store.Job) (context.Context, func(string, error)) {
	if !t.traces() && !t.metered() {
		return nil
	}
	return func(ctx context.Context, j store.Job) (context.Context, func(string, error)) {
		var span func(string, error)
		if t.tracing {
			ctx, span = traceJob(ctx, j)
		}
		var metrics func(string)
		if t.stats != nil {
			metrics = t.jobMetrics(j.Kind)
		}
		return ctx, func(outcome string, err error) {
			if span != nil {
				span(outcome, err)
			}
			if metrics != nil {
				metrics(outcome)
			}
		}
	}
}

// traceJob starts the span of one run of j.
func traceJob(ctx context.Context, j store.Job) (context.Context, func(string, error)) {
	span, ctx := tracer.StartSpanFromContext(ctx, SpanJob,
		tracer.ResourceName(string(j.Kind)),
		tracer.Tag(ext.SpanKind, ext.SpanKindInternal),
		tracer.Tag(TagOperationID, string(j.ID)),
		tracer.Tag(TagTenant, j.Bank.Tenant),
		tracer.Tag(TagOrg, j.Bank.Org),
		tracer.Tag(TagBank, j.Bank.Bank),
		tracer.Tag(TagAttempt, j.Attempt),
	)
	return ctx, func(outcome string, err error) {
		span.SetTag(TagOutcome, outcome)
		finish(span, err)
	}
}

// TickHook returns the maintenance tick tracing hook (assign it to maintenance.Deps.TraceTick): one
// SpanTick span per tick, marked as an error when a step failed. Without tracing, it returns nil.
func (t *Telemetry) TickHook() func(context.Context) (context.Context, func(error)) {
	if !t.traces() {
		return nil
	}
	return traceTick
}

// traceTick starts the span of one maintenance tick.
func traceTick(ctx context.Context) (context.Context, func(error)) {
	span, ctx := tracer.StartSpanFromContext(ctx, SpanTick, tracer.ResourceName("maintenance"),
		tracer.Tag(ext.SpanKind, ext.SpanKindInternal))
	return ctx, func(err error) { finish(span, err) }
}

// GatewayHook returns the AI gateway call hook (assign it to gateway.Config.TraceCall). While tracing: one
// SpanGatewayCall span per model call, resource = the route, tagged with the model slot and model name only
// (never the prompt, messages, tools or response). With metrics: MetricGatewayCalls, MetricGatewayLatency
// and, on failure, MetricGatewayErrors with the error class. Disabled, it returns nil.
func (t *Telemetry) GatewayHook() func(
	ctx context.Context, slot domain.ModelSlot, model, path string,
) (context.Context, func(error)) {
	if !t.traces() && !t.metered() {
		return nil
	}
	return func(ctx context.Context, slot domain.ModelSlot, model, path string) (context.Context, func(error)) {
		var span, metrics func(error)
		if t.tracing {
			ctx, span = traceGatewayCall(ctx, slot, model, path)
		}
		if t.stats != nil {
			metrics = t.gatewayMetrics(slot, model, path)
		}
		return ctx, func(err error) {
			if span != nil {
				span(err)
			}
			if metrics != nil {
				metrics(err)
			}
		}
	}
}

// traceGatewayCall starts the span of one gateway call.
func traceGatewayCall(ctx context.Context, slot domain.ModelSlot, model, path string,
) (context.Context, func(error)) {
	span, ctx := tracer.StartSpanFromContext(ctx, SpanGatewayCall,
		tracer.ResourceName(path),
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(TagModelSlot, string(slot)),
		tracer.Tag(TagModelName, model),
	)
	return ctx, func(err error) { finish(span, err) }
}

// HTTPClient returns c (nil = a new client) with a transport that adds one "http.request" span per HTTP
// attempt (method, URL without query, status) and propagates the trace to the gateway. Bodies and
// headers are never tagged. Without tracing, it returns c unchanged, nil included.
func (t *Telemetry) HTTPClient(c *http.Client) *http.Client {
	if !t.traces() {
		return c
	}
	if c == nil {
		c = &http.Client{}
	}
	return httptrace.WrapClient(c)
}

// S3APIOptions returns the AWS SDK middleware that adds one span per S3 call (resource = "S3.<Operation>",
// tagged with the bucket; object bodies are never read). Assign it to s3.Config.APIOptions. Without
// tracing, it returns nil.
func (t *Telemetry) S3APIOptions() []func(*middleware.Stack) error {
	if !t.traces() {
		return nil
	}
	var cfg aws.Config
	awstrace.AppendMiddleware(&cfg)
	return cfg.APIOptions
}

// finish ends span, marking it as an error when err is set. The recorded message is only the error's class
// (safeError): memd's error chains can quote model output or gateway response bodies.
func finish(span *tracer.Span, err error) {
	if err == nil {
		span.Finish()
		return
	}
	span.Finish(tracer.WithError(safeError(err)), tracer.NoDebugStack())
}

// errorClass is one error class a span or metric may name.
type errorClass struct {
	// err is the sentinel.
	err error
	// name is the metric tag value.
	name string
}

// errorClasses are the error classes a span or metric may name, most specific first.
var errorClasses = []errorClass{
	{context.Canceled, "canceled"},
	{context.DeadlineExceeded, "deadline_exceeded"},
	{domain.ErrCancelled, "cancelled"},
	{domain.ErrNotFound, "not_found"},
	{domain.ErrAlreadyExists, "already_exists"},
	{domain.ErrInvalidArgument, "invalid_argument"},
	{domain.ErrFailedPrecondition, "failed_precondition"},
	{domain.ErrConflict, "conflict"},
	{domain.ErrResourceExhausted, "resource_exhausted"},
	{domain.ErrUnavailable, "unavailable"},
}

// safeError reduces err to its class: the first of errorClasses it wraps, else a generic error. The
// original message never reaches the span.
func safeError(err error) error {
	for _, c := range errorClasses {
		if errors.Is(err, c.err) {
			return c.err
		}
	}
	return errUnclassified
}

// errorClassOf names err's class for a metric tag: the first of errorClasses it wraps, else "other".
func errorClassOf(err error) string {
	for _, c := range errorClasses {
		if errors.Is(err, c.err) {
			return c.name
		}
	}
	return "other"
}

// errUnclassified names an error that wraps none of the known classes.
var errUnclassified = errors.New("error (details in the memd logs)")

// TracePool installs the Postgres query tracer on cfg (before pgxpool.NewWithConfig): one
// SpanPostgresQuery span per statement (begin and commit included), resource = the SQL text, tagged with
// db.system and the rows affected; bind parameters are never recorded. Only statements whose context
// already carries a span are traced (an RPC, a job, a maintenance tick), so the worker's claim polls and
// heartbeats, which run several times a second outside any request, don't each become a root trace.
// Without tracing (disabled, or metrics only), it leaves cfg untouched.
//
// memd uses this small tracer instead of dd-trace-go's contrib/jackc/pgx.v5, which traces every statement,
// connect and acquire as its own trace when there is no parent.
func (t *Telemetry) TracePool(cfg *pgxpool.Config) {
	if !t.traces() || cfg == nil {
		return
	}
	cfg.ConnConfig.Tracer = pgxTracer{}
}

// pgxTracer is a pgx.QueryTracer that traces statements under an existing span.
type pgxTracer struct{}

var _ pgx.QueryTracer = pgxTracer{}

// pgxSpanKey is the context key of the span TraceQueryStart started (distinct from the parent span the
// context already carries, which TraceQueryEnd must never finish).
type pgxSpanKey struct{}

// TraceQueryStart starts a span for the statement when ctx carries a parent span.
func (pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if _, ok := tracer.SpanFromContext(ctx); !ok {
		return ctx
	}
	span, _ := tracer.StartSpanFromContext(ctx, SpanPostgresQuery,
		tracer.ResourceName(data.SQL),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
	)
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

// TraceQueryEnd finishes the statement's span, if TraceQueryStart started one.
func (pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(pgxSpanKey{}).(*tracer.Span)
	if !ok {
		return
	}
	if data.Err == nil {
		span.SetTag("db.row_count", data.CommandTag.RowsAffected())
	}
	finish(span, data.Err)
}
