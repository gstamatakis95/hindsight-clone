package worker

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedBanks is a ConsolidationCandidates returning the same banks every time.
type fixedBanks []port.BankRef

func (f fixedBanks) UnconsolidatedBanks(context.Context, time.Time) ([]port.BankRef, error) {
	return f, nil
}

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

func (l loggingBanks) UnconsolidatedBanks(ctx context.Context, before time.Time) ([]port.BankRef, error) {
	l.log.add("consolidation")
	return l.fixedBanks.UnconsolidatedBanks(ctx, before)
}

// Accept: three concurrent maintenance loops produce no duplicate enqueues.
func TestConcurrentMaintenanceNoDuplicateEnqueues(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	// A job whose worker died long ago: it must be reclaimed exactly once.
	stale := ops.add(retainJob("1"))
	kinds := []port.JobKind{domain.OperationKindRetainDocument}
	if js, _ := ops.Claim(context.Background(), "dead", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	ops.ageHeartbeats(DefaultStaleAfter + time.Second)
	cron, failures := &countingCron{}, &countingFailures{}
	deps := MaintenanceDeps{
		Claimer: ops, Store: ops, Clock: clock, Consolidation: fixedBanks{bankA, bankB, bankC},
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
		if n := ops.countBank(b, domain.OperationKindConsolidate, ""); n != 1 {
			t.Errorf("bank %v: %d consolidate jobs, want exactly 1", b, n)
		}
	}
	if row := ops.row(stale); row.status != domain.OperationStatusPending || row.attempt != 1 {
		t.Errorf("stale job: status %s attempt %d, want pending attempt 1 (reclaimed once)", row.status, row.attempt)
	}
	if failures.calls.Load() < 60 {
		t.Errorf("refresh-failure reconcile ran %d times, want every tick", failures.calls.Load())
	}
}

// Steps run in order, a failing step doesn't stop the rest, a deleting bank is skipped and the prune
// cut-offs come from the clock and the retentions.
func TestMaintenanceStepOrderAndIsolation(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	ops.markDeleting(bankC)
	store := &pruneStore{Store: ops}
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
	if n := ops.count(`tenant_id = $1 AND bank_id = $2`, bankA.Tenant, bankA.Bank); n != 1 {
		t.Errorf("bank A consolidate jobs = %d, want 1", n)
	}
	if n := ops.count(`tenant_id = $1 AND bank_id = $2`, bankC.Tenant, bankC.Bank); n != 0 {
		t.Errorf("deleting bank C got %d jobs", n)
	}
	now := clock.Now()
	if len(store.ops) != 1 || !store.ops[0].Equal(now.Add(-DefaultOperationRetention)) {
		t.Errorf("operations pruned before %v, want %v", store.ops, now.Add(-DefaultOperationRetention))
	}
	if len(store.evts) != 1 || !store.evts[0].Equal(now.Add(-DefaultEventRetention)) {
		t.Errorf("events pruned before %v, want %v", store.evts, now.Add(-DefaultEventRetention))
	}
	if err := RunMaintenance(context.Background(), MaintenanceDeps{}); err == nil {
		t.Error("RunMaintenance without a Claimer and Store must fail")
	}
}

// The reconcile joins a running automatic consolidate instead of queueing another one behind it.
func TestConsolidationReconcileJoinsRunning(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	ops.add(consolidateJob(bankA))
	kinds := []port.JobKind{domain.OperationKindConsolidate}
	if js, _ := ops.Claim(context.Background(), "w1", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	deps := MaintenanceDeps{
		Claimer: ops, Store: ops, Clock: clock, Consolidation: fixedBanks{bankA}, Logger: quiet,
	}.withDefaults()
	n, err := reconcileConsolidation(context.Background(), deps)
	if err != nil || n != 0 {
		t.Fatalf("reconcile = %d, %v; want 0 inserted", n, err)
	}
	if c := ops.count("true"); c != 1 {
		t.Errorf("%d consolidate rows, want 1", c)
	}
}

func TestPruneOperations(t *testing.T) {
	clock := fake.NewClock(time.Now())
	ops := newOps(t)
	done := ops.add(retainJob("old"))
	pending := ops.add(retainJob("pending"))
	recent := ops.add(retainJob("recent"))
	for _, id := range []port.OperationID{done, recent} {
		if _, err := ops.cancel(bankA, id); err != nil {
			t.Fatal(err)
		}
	}
	ops.exec(`UPDATE operations SET finished_at = now() - interval '48 hours' WHERE id = $1`, string(done))
	d := MaintenanceDeps{Store: ops, Clock: clock, OperationRetention: 24 * time.Hour}
	n, err := pruneOperations(context.Background(), d)
	if err != nil || n != 1 {
		t.Fatalf("pruneOperations = %d, %v; want 1", n, err)
	}
	if ops.count(`id = ANY($1::uuid[])`, []string{string(pending), string(recent)}) != 2 {
		t.Error("pruned a pending or recent operation")
	}
}
