package worker

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedBanks is a ConsolidationCandidates returning the same banks every time.
type fixedBanks []port.BankRef

func (f fixedBanks) BanksToConsolidate(context.Context) ([]port.BankRef, error) { return f, nil }

// stepLog records the order maintenance steps ran in.
type stepLog struct {
	mu    sync.Mutex
	steps []string
}

func (l *stepLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, s)
}

// countingCron is a CronRefresher counting calls; it fails when err is set.
type countingCron struct {
	log   *stepLog
	calls atomic.Int32
	err   error
}

func (c *countingCron) EnqueueDueCronRefreshes(context.Context, time.Time) (int, error) {
	c.calls.Add(1)
	if c.log != nil {
		c.log.add("cron")
	}
	return 0, c.err
}

// countingFailures is a RefreshFailureReconciler counting calls.
type countingFailures struct {
	log   *stepLog
	calls atomic.Int32
}

func (c *countingFailures) ReconcileFailedRefreshes(context.Context) (int, error) {
	c.calls.Add(1)
	if c.log != nil {
		c.log.add("refresh_failures")
	}
	return 0, nil
}

// loggingClaimer records ReclaimStale in the step log.
type loggingClaimer struct {
	port.Claimer
	log *stepLog
}

func (l *loggingClaimer) ReclaimStale(ctx context.Context, ttl time.Duration) (int, error) {
	l.log.add("reclaim")
	return l.Claimer.ReclaimStale(ctx, ttl)
}

// loggingBanks records the consolidation reconcile in the step log.
type loggingBanks struct {
	fixedBanks
	log *stepLog
}

func (l loggingBanks) BanksToConsolidate(ctx context.Context) ([]port.BankRef, error) {
	l.log.add("consolidation")
	return l.fixedBanks.BanksToConsolidate(ctx)
}

// Accept: three concurrent maintenance loops produce no duplicate enqueues.
func TestConcurrentMaintenanceNoDuplicateEnqueues(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	store := &fakeStore{ops: ops}
	// A job whose worker died long ago: it must be reclaimed exactly once.
	stale := ops.add(retainJob("1"))
	kinds := []port.JobKind{domain.OperationKindRetainDocument}
	if js, _ := ops.Claim(context.Background(), "dead", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	clock.Advance(DefaultStaleAfter + time.Second)
	cron, failures := &countingCron{}, &countingFailures{}
	deps := MaintenanceDeps{
		Claimer: ops, Store: store, Clock: clock, Consolidation: fixedBanks{bankA, bankB, bankC},
		CronRefresh: cron, RefreshFailures: failures, Interval: time.Millisecond, Logger: quiet,
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := RunMaintenance(ctx, deps); err != nil {
				t.Errorf("RunMaintenance: %v", err)
			}
		})
	}
	eventually(t, "60 ticks across the three loops", func() bool { return cron.calls.Load() >= 60 })
	cancel()
	wg.Wait()

	for _, b := range []port.BankRef{bankA, bankB, bankC} {
		n := ops.count(func(r *fakeRow) bool { return r.job.Bank == b && r.job.Kind == domain.OperationKindConsolidate })
		if n != 1 {
			t.Errorf("bank %v: %d consolidate jobs, want exactly 1", b, n)
		}
	}
	if row := ops.row(stale); row.status != domain.OperationStatusPending || row.job.Attempt != 1 {
		t.Errorf("stale job: status %s attempt %d, want pending attempt 1 (reclaimed once)", row.status, row.job.Attempt)
	}
	if failures.calls.Load() < 60 {
		t.Errorf("refresh-failure reconcile ran %d times, want every tick", failures.calls.Load())
	}
}

// Steps run in order, a failing step doesn't stop the rest, a deleting bank is skipped and the prune
// cut-offs come from the clock and the retentions.
func TestMaintenanceStepOrderAndIsolation(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	ops.deleting[bankC] = true
	store := &fakeStore{ops: ops}
	log := &stepLog{}
	cron := &countingCron{log: log, err: errors.New("cron parse failed")}
	deps := MaintenanceDeps{
		Claimer: &loggingClaimer{Claimer: ops, log: log}, Store: store, Clock: clock,
		Consolidation: loggingBanks{fixedBanks{bankA, bankC}, log}, CronRefresh: cron,
		RefreshFailures: &countingFailures{log: log}, Logger: quiet,
	}.withDefaults()
	err := maintenanceTick(context.Background(), deps)
	if err == nil || !strings.Contains(err.Error(), "cron_refresh") {
		t.Fatalf("tick error = %v, want the cron step's error", err)
	}
	want := []string{"reclaim", "refresh_failures", "cron", "consolidation"}
	if strings.Join(log.steps, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %v, want %v", log.steps, want)
	}
	if n := ops.count(func(r *fakeRow) bool { return r.job.Bank == bankA }); n != 1 {
		t.Errorf("bank A consolidate jobs = %d, want 1", n)
	}
	if n := ops.count(func(r *fakeRow) bool { return r.job.Bank == bankC }); n != 0 {
		t.Errorf("deleting bank C got %d jobs", n)
	}
	now := clock.Now()
	if len(ops.prunedAt) != 1 || !ops.prunedAt[0].Equal(now.Add(-DefaultOperationRetention)) {
		t.Errorf("operations pruned before %v, want %v", ops.prunedAt, now.Add(-DefaultOperationRetention))
	}
	if len(store.eventsPrunedBefore) != 1 || !store.eventsPrunedBefore[0].Equal(now.Add(-DefaultEventRetention)) {
		t.Errorf("events pruned before %v, want %v", store.eventsPrunedBefore, now.Add(-DefaultEventRetention))
	}
	if err := RunMaintenance(context.Background(), MaintenanceDeps{}); err == nil {
		t.Error("RunMaintenance without a Claimer and Store must fail")
	}
}

// The reconcile joins a running automatic consolidate instead of queueing another one behind it.
func TestConsolidationReconcileJoinsRunning(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	ops.add(consolidateJob(bankA))
	kinds := []port.JobKind{domain.OperationKindConsolidate}
	if js, _ := ops.Claim(context.Background(), "w1", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	deps := MaintenanceDeps{
		Claimer: ops, Store: &fakeStore{ops: ops}, Clock: clock, Consolidation: fixedBanks{bankA}, Logger: quiet,
	}.withDefaults()
	n, err := reconcileConsolidation(context.Background(), deps)
	if err != nil || n != 0 {
		t.Fatalf("reconcile = %d, %v; want 0 inserted", n, err)
	}
	if c := ops.count(func(*fakeRow) bool { return true }); c != 1 {
		t.Errorf("%d consolidate rows, want 1", c)
	}
}

func TestPruneOperations(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	done := ops.add(retainJob("old"))
	pending := ops.add(retainJob("pending"))
	if _, err := ops.Cancel(context.Background(), bankA, done); err != nil {
		t.Fatal(err)
	}
	clock.Advance(48 * time.Hour)
	recent := ops.add(retainJob("recent"))
	if _, err := ops.Cancel(context.Background(), bankA, recent); err != nil {
		t.Fatal(err)
	}
	n, err := PruneOperations(context.Background(), &fakeStore{ops: ops}, clock, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("PruneOperations = %d, %v; want 1", n, err)
	}
	if ops.count(func(r *fakeRow) bool { return r.job.ID == pending || r.job.ID == recent }) != 2 {
		t.Error("pruned a pending or recent operation")
	}
}
