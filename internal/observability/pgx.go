package observability

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
