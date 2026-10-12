package maintenance

import (
	"context"
	"errors"
	"memd/internal/bank"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/mentalmodel"
	"memd/internal/observation"
	"memd/internal/store"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedBanks is a store.Maintenance whose UnconsolidatedBanks returns the same banks every time (the step it is
// wired into calls nothing else).
type fixedBanks struct {
	store.Maintenance
	banks []domain.BankRef
}

func (f fixedBanks) UnconsolidatedBanks(context.Context, time.Time) ([]store.UnconsolidatedBank, error) {
	out := make([]store.UnconsolidatedBank, len(f.banks))
	for i, b := range f.banks {
		out[i] = store.UnconsolidatedBank{Bank: b} // the zero time: waiting longer than any grace
	}
	return out, nil
}

// banksOf returns a fixedBanks over bs.
func banksOf(bs ...domain.BankRef) fixedBanks { return fixedBanks{banks: bs} }

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

// countingModels is the store.Maintenance the mental-model sweeps read: it counts and logs the cron sweep's
// CronModels and the failure reconcile's FailedRefreshes (both empty), and fails CronModels when cronErr is set.
type countingModels struct {
	store.Maintenance
	log      *stepLog
	cron     atomic.Int32
	failures atomic.Int32
	cronErr  error
}

func (c *countingModels) CronModels(context.Context) ([]store.CronModel, error) {
	c.cron.Add(1)
	if c.log != nil {
		c.log.add("cron")
	}
	return nil, c.cronErr
}

func (c *countingModels) FailedRefreshes(context.Context) ([]store.FailedRefresh, error) {
	c.failures.Add(1)
	if c.log != nil {
		c.log.add("refresh_failures")
	}
	return nil, nil
}

// observations is an observation.Service over ops whose reconcile sweep lists banks (every bank's config
// is the default).
func observations(ops *opsDB, banks store.Maintenance, c clock.Clock) *observation.Service {
	return observation.New(observation.Deps{
		DB: ops, Maintenance: banks, Config: config.NewResolver(ops, domain.BankConfig{}), Clock: c,
	})
}

// mentalModels is a mentalmodel.Service over ops whose sweeps read m.
func mentalModels(ops *opsDB, m *countingModels, c clock.Clock) *mentalmodel.Service {
	return mentalmodel.New(mentalmodel.Deps{DB: ops, Maintenance: m, Clock: c})
}

// loggingClaimer records ReclaimStale in the step log.
type loggingClaimer struct {
	jobs.Claimer
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

func (l loggingBanks) UnconsolidatedBanks(ctx context.Context, before time.Time) ([]store.UnconsolidatedBank, error) {
	l.log.add("consolidation")
	return l.fixedBanks.UnconsolidatedBanks(ctx, before)
}

// Accept: three concurrent maintenance loops produce no duplicate enqueues.
func TestConcurrentMaintenanceNoDuplicateEnqueues(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	// A job whose worker died long ago: it must be reclaimed exactly once.
	stale := ops.add(retainJob("1"))
	kinds := []domain.OperationKind{domain.OperationKindRetainDocument}
	if js, _ := ops.Claim(context.Background(), "dead", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	ops.ageHeartbeats(jobs.DefaultStaleAfter + time.Second)
	models := &countingModels{}
	deps := Deps{
		Claimer: ops, Store: ops, Clock: clock,
		Observations: observations(ops, banksOf(bankA, bankB, bankC), clock),
		MentalModels: mentalModels(ops, models, clock), Config: Config{Interval: time.Millisecond}, Logger: quiet,
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := New(deps).Run(ctx); err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	eventually(t, "60 ticks across the three loops", func() bool { return models.cron.Load() >= 60 })
	cancel()
	wg.Wait()

	for _, b := range []domain.BankRef{bankA, bankB, bankC} {
		if n := ops.countBank(b, domain.OperationKindConsolidate, ""); n != 1 {
			t.Errorf("bank %v: %d consolidate jobs, want exactly 1", b, n)
		}
	}
	if row := ops.row(stale); row.status != domain.OperationStatusPending || row.attempt != 1 {
		t.Errorf("stale job: status %s attempt %d, want pending attempt 1 (reclaimed once)", row.status, row.attempt)
	}
	if models.failures.Load() < 60 {
		t.Errorf("refresh-failure reconcile ran %d times, want every tick", models.failures.Load())
	}
}

// Steps run in order, a failing step doesn't stop the rest, a deleting bank is skipped and the prune
// cut-offs come from the clock and the retentions.
func TestMaintenanceStepOrderAndIsolation(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	ops.markDeleting(bankC)
	store := &pruneStore{DB: ops}
	log := &stepLog{}
	models := &countingModels{log: log, cronErr: errors.New("cron models failed")}
	deps := New(Deps{
		Claimer: &loggingClaimer{Claimer: ops, log: log}, Store: store, Clock: clock,
		Observations: observations(ops, loggingBanks{banksOf(bankA, bankC), log}, clock),
		MentalModels: mentalModels(ops, models, clock), Logger: quiet,
	})
	err := deps.tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cron_refresh") {
		t.Fatalf("tick error = %v, want the cron step's error", err)
	}
	want := []string{"reclaim", "refresh_failures", "cron", "consolidation"}
	if strings.Join(log.steps, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %v, want %v", log.steps, want)
	}
	if n := ops.count(`tenant_id = $1 AND org_id = $2 AND bank_id = $3`, bankA.Tenant, bankA.Org, bankA.Bank); n != 1 {
		t.Errorf("bank A consolidate jobs = %d, want 1", n)
	}
	if n := ops.count(`tenant_id = $1 AND org_id = $2 AND bank_id = $3`, bankC.Tenant, bankC.Org, bankC.Bank); n != 0 {
		t.Errorf("deleting bank C got %d jobs", n)
	}
	now := clock.Now()
	if len(store.ops) != 1 || !store.ops[0].Equal(now.Add(-DefaultOperationRetention)) {
		t.Errorf("operations pruned before %v, want %v", store.ops, now.Add(-DefaultOperationRetention))
	}
	if len(store.evts) != 1 || !store.evts[0].Equal(now.Add(-DefaultEventRetention)) {
		t.Errorf("events pruned before %v, want %v", store.evts, now.Add(-DefaultEventRetention))
	}
	if err := New(Deps{}).Run(context.Background()); err == nil {
		t.Error("Run without a Claimer and Store must fail")
	}
}

// The reconcile joins a running automatic consolidate instead of queueing another one behind it.
func TestConsolidationReconcileJoinsRunning(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	ops.add(consolidateJob(bankA))
	kinds := []domain.OperationKind{domain.OperationKindConsolidate}
	if js, _ := ops.Claim(context.Background(), "w1", kinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Observations: observations(ops, banksOf(bankA), clock), Logger: quiet,
	})
	n, err := deps.reconcileConsolidation(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("reconcile = %d, %v; want 0 inserted", n, err)
	}
	if c := ops.count("true"); c != 1 {
		t.Errorf("%d consolidate rows, want 1", c)
	}
}

func TestPruneOperations(t *testing.T) {
	clock := clock.NewManual(time.Now())
	ops := newOps(t)
	done := ops.add(retainJob("old"))
	pending := ops.add(retainJob("pending"))
	recent := ops.add(retainJob("recent"))
	for _, id := range []domain.OperationID{done, recent} {
		if _, err := ops.cancel(bankA, id); err != nil {
			t.Fatal(err)
		}
	}
	ops.exec(`UPDATE operations SET finished_at = now() - interval '48 hours' WHERE id = $1`, string(done))
	d := New(Deps{Store: ops, Clock: clock, Config: Config{OperationRetention: 24 * time.Hour}})
	n, err := d.pruneOperations(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("pruneOperations = %d, %v; want 1", n, err)
	}
	if ops.count(`id = ANY($1::uuid[])`, []string{string(pending), string(recent)}) != 2 {
		t.Error("pruned a pending or recent operation")
	}
}

// graphBanks is a store.Maintenance whose BanksWithGraphWork returns fixed banks.
type graphBanks struct {
	store.Maintenance
	banks []domain.BankRef
}

func (g graphBanks) BanksWithGraphWork(context.Context) ([]domain.BankRef, error) {
	return g.banks, nil
}

// A delete_bank that ReclaimStale fails at its attempt cap is re-enqueued by the
// same tick's resume step, and later ticks join that row instead of adding more.
func TestMaintenanceResumesStalledBankDelete(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	limit := domain.OperationKindDeleteBank.MaxAttempts()
	if limit <= 0 {
		t.Fatalf("delete_bank is uncapped (%d); the test needs the cap", limit)
	}
	first := ops.add(store.Job{
		Bank: bankC, Kind: domain.OperationKindDeleteBank, SerialKey: jobs.SerialKeyDeleteBank,
		Attempt: limit - 1, Dedupe: store.DedupePendingOrRunning,
	})
	deleteKinds := []domain.OperationKind{domain.OperationKindDeleteBank}
	if js, _ := ops.Claim(context.Background(), "dead", deleteKinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	ops.markDeleting(bankC)
	ops.ageHeartbeats(jobs.DefaultStaleAfter + time.Second)
	banks := bank.New(bank.Deps{DB: ops, Maintenance: ops, Clock: clock})
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Logger: quiet, Banks: banks,
	})
	for range 3 {
		if err := deps.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if row := ops.row(first); row.status != domain.OperationStatusFailed {
		t.Fatalf("reclaimed delete_bank at its cap: %s, want failed", row.status)
	}
	pending := ops.countBank(bankC, domain.OperationKindDeleteBank, "status = 'pending' AND serial_key = $5",
		jobs.SerialKeyDeleteBank)
	if pending != 1 {
		t.Fatalf("%d pending delete_bank rows after three ticks, want exactly 1", pending)
	}
	js, _ := ops.Claim(context.Background(), "w", []domain.OperationKind{domain.OperationKindDeleteBank}, 1)
	if len(js) != 1 || js[0].Attempt != 0 {
		t.Fatalf("resumed delete_bank not claimable as a fresh job: %+v", js)
	}
}

// The tick enqueues graph_maintenance for banks with graph work, joins a queued or
// running one, and skips a deleting bank.
func TestMaintenanceEnqueuesGraphMaintenance(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	ops.markDeleting(bankC)
	running := ops.add(store.Job{
		Bank: bankB, Kind: domain.OperationKindGraphMaintenance, SerialKey: jobs.SerialKeyGraph,
		Dedupe: store.DedupePendingOrRunning,
	})
	graphKinds := []domain.OperationKind{domain.OperationKindGraphMaintenance}
	if js, _ := ops.Claim(context.Background(), "w", graphKinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Logger: quiet,
		Graph: graphBanks{banks: []domain.BankRef{bankA, bankB, bankC}},
	})
	n, err := deps.reconcileGraph(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v; want 1 inserted (bank A)", n, err)
	}
	if n, err := deps.reconcileGraph(context.Background()); err != nil || n != 0 {
		t.Fatalf("second reconcile = %d, %v; want 0 (joined)", n, err)
	}
	for b, want := range map[domain.BankRef]int{bankA: 1, bankB: 1, bankC: 0} {
		got := ops.countBank(b, domain.OperationKindGraphMaintenance, "serial_key = $5", jobs.SerialKeyGraph)
		if got != want {
			t.Errorf("bank %v: %d graph_maintenance rows, want %d", b, got, want)
		}
	}
	if row := ops.row(running); row.status != domain.OperationStatusProcessing {
		t.Errorf("running graph job: %s", row.status)
	}
}
