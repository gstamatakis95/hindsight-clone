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
)

// JobHook returns the worker's job tracing hook (assign it to worker.RunnerConfig.TraceJob): one SpanJob
// span per run, resource = the job kind, tagged with the operation id, tenant, bank, attempt and outcome,
// and marked as an error when the run did not complete. Disabled, it returns nil.
func (t *Telemetry) JobHook() func(context.Context, store.Job) (context.Context, func(string, error)) {
	if t == nil {
		return nil
	}
	return traceJob
}

// traceJob starts the span of one run of j.
func traceJob(ctx context.Context, j store.Job) (context.Context, func(string, error)) {
	span, ctx := tracer.StartSpanFromContext(ctx, SpanJob,
		tracer.ResourceName(string(j.Kind)),
		tracer.Tag(ext.SpanKind, ext.SpanKindInternal),
		tracer.Tag(TagOperationID, string(j.ID)),
		tracer.Tag(TagTenant, j.Bank.Tenant),
		tracer.Tag(TagBank, j.Bank.Bank),
		tracer.Tag(TagAttempt, j.Attempt),
	)
	return ctx, func(outcome string, err error) {
		span.SetTag(TagOutcome, outcome)
		finish(span, err)
	}
}

// TickHook returns the maintenance tick tracing hook (assign it to worker.MaintenanceDeps.TraceTick): one
// SpanTick span per tick, marked as an error when a step failed. Disabled, it returns nil.
func (t *Telemetry) TickHook() func(context.Context) (context.Context, func(error)) {
	if t == nil {
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

// GatewayHook returns the AI gateway tracing hook (assign it to gateway.Config.TraceCall): one
// SpanGatewayCall span per model call, resource = the route, tagged with the model slot and model name
// only (never the prompt, messages, tools or response). Disabled, it returns nil.
func (t *Telemetry) GatewayHook() func(
	ctx context.Context, slot domain.ModelSlot, model, path string,
) (context.Context, func(error)) {
	if t == nil {
		return nil
	}
	return traceGatewayCall
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
// headers are never tagged. Disabled, it returns c unchanged, nil included.
func (t *Telemetry) HTTPClient(c *http.Client) *http.Client {
	if t == nil {
		return c
	}
	if c == nil {
		c = &http.Client{}
	}
	return httptrace.WrapClient(c)
}

// S3APIOptions returns the AWS SDK middleware that adds one span per S3 call (resource = "S3.<Operation>",
// tagged with the bucket; object bodies are never read). Assign it to s3.Config.APIOptions. Disabled, it
// returns nil.
func (t *Telemetry) S3APIOptions() []func(*middleware.Stack) error {
	if t == nil {
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

// errorClasses are the error classes a span may name, most specific first.
var errorClasses = []error{
	context.Canceled, context.DeadlineExceeded, domain.ErrCancelled, domain.ErrNotFound,
	domain.ErrAlreadyExists, domain.ErrInvalidArgument, domain.ErrFailedPrecondition, domain.ErrConflict,
	domain.ErrResourceExhausted, domain.ErrUnavailable,
}

// safeError reduces err to its class: the first of errorClasses it wraps, else a generic error. The
// original message never reaches the span.
func safeError(err error) error {
	for _, c := range errorClasses {
		if errors.Is(err, c) {
			return c
		}
	}
	return errUnclassified
}

// errUnclassified names an error that wraps none of the known classes.
var errUnclassified = errors.New("error (details in the memd logs)")
