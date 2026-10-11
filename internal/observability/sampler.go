package observability

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sampleTimeout bounds one queue read of the sampler.
const sampleTimeout = 5 * time.Second

// QueueReader reads the job queue's depth; store.Maintenance satisfies it.
type QueueReader interface {
	// QueueStats returns the pending and processing jobs by kind and status, with the age of each kind's oldest
	// due pending job.
	QueueStats(ctx context.Context) ([]store.QueueStat, error)
}

// Sources are what the metrics sampler reads; a nil field is not sampled.
type Sources struct {
	// Queue feeds MetricQueueDepth and MetricQueueOldestAge. cmd/memd sets it in worker and all modes only:
	// the queue is global, so api replicas would only repeat the workers' numbers.
	Queue QueueReader
	// Pool returns the Postgres pool's statistics (pgxpool.Pool.Stat), which feed the postgres.pool metrics.
	Pool func() *pgxpool.Stat
}

// StartSampler samples src now and then every interval in the background and sends the gauges, until ctx ends
// or the returned stop func is called; stop waits for a running sample to end, so call it before the pool
// closes and before Start's stop closes the metrics client. Without metrics (or with a non-positive interval)
// it starts nothing and stop is a no-op.
func (t *Telemetry) StartSampler(ctx context.Context, interval time.Duration, src Sources) (stop func()) {
	if !t.metered() || interval <= 0 || (src.Queue == nil && src.Pool == nil) {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &sampler{t: t, src: src, depth: map[[2]string]bool{}, age: map[domain.OperationKind]bool{}}
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			s.sample(ctx)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
	return func() {
		cancel()
		wg.Wait()
	}
}

// sampler holds what the gauges need between samples.
type sampler struct {
	// t sends the metrics.
	t *Telemetry
	// src are the sources.
	src Sources
	// depth are the (kind, status) pairs ever reported; a pair missing from a sample is sent as 0, so a
	// drained queue reads 0 rather than going silent.
	depth map[[2]string]bool
	// age are the kinds whose oldest pending age was ever reported, likewise sent as 0 when none is due.
	age map[domain.OperationKind]bool
	// last are the pool's cumulative counters at the previous sample, so the counts carry the difference.
	last poolCounters
}

// poolCounters are pgxpool's cumulative counters.
type poolCounters struct {
	acquires, empty, canceled int64
	emptyWait                 time.Duration
}

// sample sends one round of gauges.
func (s *sampler) sample(ctx context.Context) {
	if s.src.Pool != nil {
		s.samplePool(s.src.Pool())
	}
	if s.src.Queue != nil {
		s.sampleQueue(ctx)
	}
}

// samplePool sends the pool's gauges and the growth of its counters since the previous sample.
func (s *sampler) samplePool(st *pgxpool.Stat) {
	t := s.t
	t.gauge(MetricPoolAcquired, float64(st.AcquiredConns()))
	t.gauge(MetricPoolIdle, float64(st.IdleConns()))
	t.gauge(MetricPoolTotal, float64(st.TotalConns()))
	t.gauge(MetricPoolMax, float64(st.MaxConns()))
	now := poolCounters{
		acquires: st.AcquireCount(), empty: st.EmptyAcquireCount(), canceled: st.CanceledAcquireCount(),
		emptyWait: st.EmptyAcquireWaitTime(),
	}
	t.count(MetricPoolAcquires, now.acquires-s.last.acquires)
	t.count(MetricPoolEmptyAcquires, now.empty-s.last.empty)
	t.count(MetricPoolCanceledAcquires, now.canceled-s.last.canceled)
	t.count(MetricPoolEmptyAcquireWait, (now.emptyWait - s.last.emptyWait).Milliseconds())
	s.last = now
}

// sampleQueue sends the queue's depth by kind and status and each kind's oldest pending age; a failed read is
// logged and skipped.
func (s *sampler) sampleQueue(ctx context.Context) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	stats, err := s.src.Queue.QueueStats(qctx)
	if err != nil {
		if ctx.Err() == nil {
			s.t.log.Warn("datadog: queue metrics not sampled", "err", err)
		}
		return
	}
	depth := map[[2]string]int{}
	age := map[domain.OperationKind]time.Duration{}
	for _, q := range stats {
		depth[[2]string{string(q.Kind), string(q.Status)}] = q.Jobs
		if q.Status == domain.OperationStatusPending {
			age[q.Kind] = q.OldestDue
		}
	}
	for k := range depth {
		s.depth[k] = true
	}
	for k := range age {
		s.age[k] = true
	}
	for k := range s.depth {
		s.t.gauge(MetricQueueDepth, float64(depth[k]), tag("kind", k[0]), tag("status", k[1]))
	}
	for k := range s.age {
		s.t.gauge(MetricQueueOldestAge, age[k].Seconds(), tag("kind", string(k)))
	}
}
