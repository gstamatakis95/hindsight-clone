package observability

import (
	"context"
	"memd/internal/domain"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// ingestEventMetrics maps the queue consumer's events to their counters. The keys are the values of
// ingestapi.EventReceived, EventAcked, EventNacked, EventDeadLettered and EventDropped, written out so this package
// doesn't depend on the consumer; keep them in sync.
var ingestEventMetrics = map[string]string{
	"received":      MetricIngestReceived,
	"acked":         MetricIngestAcked,
	"nacked":        MetricIngestNacked,
	"dead_lettered": MetricIngestDeadLettered,
	"dropped":       MetricIngestDropped,
}

// IngestBatchHook returns the queue consumer's batch hook (assign it to ingestapi.Hooks.Batch). While tracing:
// one SpanIngestBatch span per RetainBatch call, tagged with the tenant, org, bank and message count, and marked as
// an error when the call failed. With metrics: MetricIngestBatchLatency, tagged with the error class only.
// Disabled, it returns nil.
func (t *Telemetry) IngestBatchHook() func(context.Context, domain.BankRef, int) (context.Context, func(error)) {
	if !t.traces() && !t.metered() {
		return nil
	}
	return func(ctx context.Context, b domain.BankRef, items int) (context.Context, func(error)) {
		start := time.Now()
		var span *tracer.Span
		if t.tracing {
			span, ctx = tracer.StartSpanFromContext(ctx, SpanIngestBatch,
				tracer.ResourceName("retain_batch"),
				tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
				tracer.Tag(TagTenant, b.Tenant),
				tracer.Tag(TagOrg, b.Org),
				tracer.Tag(TagBank, b.Bank),
				tracer.Tag(TagIngestItems, items),
			)
		}
		return ctx, func(err error) {
			if span != nil {
				finish(span, err)
			}
			if t.stats != nil {
				class := "none"
				if err != nil {
					class = errorClassOf(err)
				}
				t.millis(MetricIngestBatchLatency, time.Since(start), tag("error", class))
			}
		}
	}
}

// IngestCountHook returns the queue consumer's counting hook (assign it to ingestapi.Hooks.Count): it adds n to
// the event's counter, tagged with the reason code when there is one. Without metrics it returns nil.
func (t *Telemetry) IngestCountHook() func(event, reason string, n int) {
	if !t.metered() {
		return nil
	}
	return func(event, reason string, n int) {
		name, ok := ingestEventMetrics[event]
		if !ok {
			return
		}
		if reason == "" {
			t.count(name, int64(n))
			return
		}
		t.count(name, int64(n), tag("reason", reason))
	}
}
