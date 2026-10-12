package ingestapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/ingest"
	"memd/internal/memory"
	"memd/internal/org"
	"time"

	"golang.org/x/sync/errgroup"
)

// Defaults of the Config fields left zero.
const (
	// DefaultBatchSize is how many messages one Receive asks for, and the most items of one RetainBatch call.
	DefaultBatchSize = 100
	// DefaultFlushInterval is how long the consumer keeps receiving to fill a batch.
	DefaultFlushInterval = 500 * time.Millisecond
	// DefaultConcurrency is how many RetainBatch calls run at once.
	DefaultConcurrency = 4
	// DefaultMaxAttempts is the delivery count at which a message that still fails is dead-lettered.
	DefaultMaxAttempts = 10
	// DefaultBackoffInitial is the redelivery delay after a first failed delivery.
	DefaultBackoffInitial = time.Second
	// DefaultBackoffMax caps the redelivery delay.
	DefaultBackoffMax = 5 * time.Minute
	// DefaultShutdownGrace is how long a stopping consumer may spend retaining and settling what it holds.
	DefaultShutdownGrace = 20 * time.Second
)

// Config is the consumer's behaviour: the ingest.consumer section of the server config file, the ingest limits
// RetainBatch enforces, and the tenant and org the source is pinned to.
type Config struct {
	// BatchSize is how many messages one Receive asks for and the most items one RetainBatch call carries
	// (DefaultBatchSize when 0). It must not exceed ingest.max_batch_items.
	BatchSize int
	// FlushInterval is how long the consumer keeps receiving to fill a batch once it holds a message
	// (DefaultFlushInterval when 0). An idle poll flushes at once.
	FlushInterval time.Duration
	// Concurrency is how many RetainBatch calls run at once (DefaultConcurrency when 0).
	Concurrency int
	// MaxAttempts is the delivery count at which a message whose retain still fails is dead-lettered
	// (DefaultMaxAttempts when 0). A source that reports no attempt count never reaches it.
	MaxAttempts int
	// BackoffInitial is the redelivery delay after a first failure; it doubles per delivery
	// (DefaultBackoffInitial when 0).
	BackoffInitial time.Duration
	// BackoffMax caps the redelivery delay (DefaultBackoffMax when 0).
	BackoffMax time.Duration
	// ShutdownGrace bounds how long Run, once its context ends, may spend retaining and settling the messages it
	// holds (DefaultShutdownGrace when 0). What is still unsettled after it is redelivered later.
	ShutdownGrace time.Duration
	// MaxRequestBytes is ingest.max_request_bytes: a RetainBatch call's summed content stays within it.
	// 0 = memory.DefaultMaxRequestBytes.
	MaxRequestBytes int
	// Tenant, when set, pins the source to one tenant: an envelope without tenant_id is that tenant's, and one
	// naming another tenant is dead-lettered (TENANT_MISMATCH). Unset, every envelope must name its tenant, and
	// the broker's publish permissions are the tenant boundary.
	Tenant string
	// Org, when set, pins the source to one org the same way (ORG_MISMATCH).
	Org string
}

// withDefaults returns c with the zero fields filled in.
func (c Config) withDefaults() Config {
	fill := func(v *int, def int) {
		if *v == 0 {
			*v = def
		}
	}
	fillD := func(v *time.Duration, def time.Duration) {
		if *v == 0 {
			*v = def
		}
	}
	fill(&c.BatchSize, DefaultBatchSize)
	fill(&c.Concurrency, DefaultConcurrency)
	fill(&c.MaxAttempts, DefaultMaxAttempts)
	fill(&c.MaxRequestBytes, memory.DefaultMaxRequestBytes)
	fillD(&c.FlushInterval, DefaultFlushInterval)
	fillD(&c.BackoffInitial, DefaultBackoffInitial)
	fillD(&c.BackoffMax, DefaultBackoffMax)
	fillD(&c.ShutdownGrace, DefaultShutdownGrace)
	return c
}

// Hooks are the consumer's optional observability callbacks (cmd/memd sets them from internal/observability);
// a nil field is skipped.
type Hooks struct {
	// Batch wraps one RetainBatch call of items messages for bank: the call runs under the returned context
	// (which may carry a span), and done receives the call's error.
	Batch func(ctx context.Context, bank domain.BankRef, items int) (context.Context, func(err error))
	// Count records that n messages reached event (EventReceived, EventAcked, EventNacked, EventDeadLettered or
	// EventDropped), with reason, a bounded code, for every event but received and acked.
	Count func(event, reason string, n int)
}

// Deps is what a Consumer works with.
type Deps struct {
	// Source is the subscription. Run closes it when it returns.
	Source ingest.Source
	// Memory runs RetainBatch, the path the RPC shares.
	Memory *memory.Service
	// Orgs tells a deleted org (one with a deletion receipt) from one not created yet, so a message for an org
	// deleted since it was published is dropped rather than retried. nil = every unknown org is retried.
	Orgs *org.Service
	// Clock times the flush interval.
	Clock clock.Clock
	// Logger receives the consumer's warnings; nil = slog.Default().
	Logger *slog.Logger
	// Hooks are the observability callbacks.
	Hooks Hooks
}

// Consumer drives one ingest.Source: it receives messages, retains them through memory.Service.RetainBatch and
// settles each one from its result. The package comment has the full behaviour.
type Consumer struct {
	cfg Config
	d   Deps
}

// New returns a Consumer over d with cfg (zero fields take their defaults).
func New(cfg Config, d Deps) *Consumer {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	return &Consumer{cfg: cfg.withDefaults(), d: d}
}

// Run consumes until ctx ends or the source closes, then stops receiving, retains and settles the messages it
// holds (within Config.ShutdownGrace) and closes the source. A broker error from Receive is logged and retried
// after Config.BackoffInitial; Run returns only the error of closing the source.
func (c *Consumer) Run(ctx context.Context) error {
	work, stop := c.workContext(ctx)
	defer stop()
	for {
		msgs, err := c.fill(ctx)
		if len(msgs) > 0 {
			if pause := c.process(work, msgs); pause > 0 && ctx.Err() == nil {
				c.d.Logger.Warn("ingest: retain backlog over its limit; pausing", "for", pause)
				wait(ctx, pause)
			}
		}
		if ctx.Err() != nil || errors.Is(err, ingest.ErrClosed) {
			break
		}
		if err != nil {
			c.d.Logger.Warn("ingest: receive failed; retrying", "err", err, "after", c.cfg.BackoffInitial)
			wait(ctx, c.cfg.BackoffInitial)
		}
	}
	if err := c.d.Source.Close(); err != nil {
		return fmt.Errorf("ingest: close source: %w", err)
	}
	return nil
}

// workContext returns the context retains and settlements run under: it outlives ctx by Config.ShutdownGrace, so
// a stop signal lets the messages already received finish instead of failing them. stop releases it.
func (c *Consumer) workContext(ctx context.Context) (context.Context, func()) {
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		t := time.NewTimer(c.cfg.ShutdownGrace)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			cancel()
		}
	}()
	return work, func() { close(done); cancel() }
}

// fill receives up to Config.BatchSize messages: it keeps calling Receive while messages keep arriving, until the
// batch is full or Config.FlushInterval has passed since the first one. An empty poll returns what it holds. A
// Receive error returns it together with the messages already received, which the caller still processes.
func (c *Consumer) fill(ctx context.Context) ([]ingest.Message, error) {
	var buf []ingest.Message
	var first time.Time
	for len(buf) < c.cfg.BatchSize {
		msgs, err := c.d.Source.Receive(ctx, c.cfg.BatchSize-len(buf))
		if err != nil {
			return buf, err //nolint:wrapcheck // ingest errors are domain classes or ErrClosed, tested by the caller
		}
		if len(msgs) == 0 {
			break
		}
		if first.IsZero() {
			first = c.d.Clock.Now()
		}
		buf = append(buf, msgs...)
		if c.d.Clock.Now().Sub(first) >= c.cfg.FlushInterval {
			break
		}
	}
	return buf, nil
}

// process retains msgs and settles every one of them. Envelopes that can't be used are settled at once; the
// rest are grouped into RetainBatch calls (packBatches), run up to Config.Concurrency at a time. It returns how
// long to pause receiving: the longest retry delay of a backlog rejection, or 0.
func (c *Consumer) process(ctx context.Context, msgs []ingest.Message) time.Duration {
	c.count(EventReceived, "", len(msgs))
	var batches []*batch
	var bad settlements
	for _, m := range msgs {
		it, d := c.decode(m)
		if it == nil {
			bad.add(m, d)
			continue
		}
		batches = c.packBatches(batches, it)
	}
	c.settle(ctx, bad)
	pauses := make([]time.Duration, len(batches))
	var g errgroup.Group
	g.SetLimit(c.cfg.Concurrency)
	for k, b := range batches {
		g.Go(func() error {
			pauses[k] = c.retain(ctx, b)
			return nil
		})
	}
	_ = g.Wait() // every call returns nil; outcomes are settled per message
	var pause time.Duration
	for _, p := range pauses {
		pause = max(pause, p)
	}
	return pause
}

// retain runs one batch through RetainBatch, decides every message's settlement and applies them. It returns the
// batch's pause (see process).
func (c *Consumer) retain(ctx context.Context, b *batch) time.Duration {
	done := func(error) {}
	if c.d.Hooks.Batch != nil {
		ctx, done = c.d.Hooks.Batch(ctx, b.req.Bank, len(b.msgs))
	}
	res, err := c.d.Memory.RetainBatch(ctx, b.req)
	done(err)
	gone := c.orgGone(ctx, b.req.Bank, err, res)
	var out settlements
	var pause time.Duration
	for k, m := range b.msgs {
		st, itemErr := memory.BatchUnspecified, err
		if err == nil {
			st, itemErr = res.Items[k].Status, res.Items[k].Err
		}
		d := decide(st, itemErr, m.Attempt, gone, c.cfg)
		pause = max(pause, d.pause)
		out.add(m, d)
	}
	c.settle(ctx, out)
	return pause
}

// orgGone reports whether the batch's org was not found (by the call or by an item) and is deleted: it is still
// not live and has a deletion receipt. A live org wins over a receipt, since a receipt outlives the re-creation of
// its org id. A failed lookup counts as not deleted: the messages are retried.
func (c *Consumer) orgGone(ctx context.Context, b domain.BankRef, err error, res memory.RetainBatchResult) bool {
	if c.d.Orgs == nil {
		return false
	}
	notFound := errors.Is(err, domain.ErrOrgNotFound)
	for _, it := range res.Items {
		notFound = notFound || errors.Is(it.Err, domain.ErrOrgNotFound)
	}
	if !notFound {
		return false
	}
	deleted, lerr := c.orgDeleted(ctx, b.OrgRef())
	if lerr != nil {
		c.d.Logger.Warn("ingest: deletion receipt lookup failed; retrying the org's messages", "err", lerr)
		return false
	}
	return deleted
}

// orgDeleted reports whether o is not live (unknown or being deleted) and has a deletion receipt.
func (c *Consumer) orgDeleted(ctx context.Context, o domain.OrgRef) (bool, error) {
	_, err := c.d.Orgs.GetOrg(ctx, o)
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, domain.ErrOrgNotFound):
		return false, err //nolint:wrapcheck // org errors are domain error classes; logged by the caller
	}
	_, err = c.d.Orgs.GetDeletionReceipt(ctx, o)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, domain.ErrNotFound):
		return false, nil
	default:
		return false, err //nolint:wrapcheck // org errors are domain error classes; logged by the caller
	}
}

// count calls the Count hook when there is one and n > 0.
func (c *Consumer) count(event, reason string, n int) {
	if c.d.Hooks.Count != nil && n > 0 {
		c.d.Hooks.Count(event, reason, n)
	}
}

// wait blocks for d or until ctx ends.
func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
