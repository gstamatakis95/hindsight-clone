package worker

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/port"
	"testing"
	"time"
)

// bankConfigs is a port.ConfigResolver serving a fixed effective config per bank; unknown banks are
// domain.ErrNotFound.
type bankConfigs map[port.BankRef]domain.BankConfig

func (c bankConfigs) Effective(_ context.Context, b port.BankRef, _ ...domain.BankConfig) (domain.BankConfig, error) {
	cfg, ok := c[b]
	if !ok {
		return domain.BankConfig{}, domain.ErrNotFound
	}
	return cfg, nil
}

func (c bankConfigs) ForScope(context.Context, port.ScopeKey, *domain.BankConfig) (domain.BankConfig, error) {
	panic("not used")
}

// cutoffBanks is a ConsolidationCandidates recording the cut-off it was asked for.
type cutoffBanks struct {
	banks  []port.BankRef
	before []time.Time
}

func (c *cutoffBanks) UnconsolidatedBanks(_ context.Context, before time.Time) ([]port.BankRef, error) {
	c.before = append(c.before, before)
	return c.banks, nil
}

// opsDeletes is a StalledDeleteSource computed from the fake operations table: deleting banks with no
// pending or processing delete_bank row.
type opsDeletes struct{ ops *fakeOps }

func (o opsDeletes) StalledBankDeletes(context.Context) ([]port.BankRef, error) {
	o.ops.mu.Lock()
	defer o.ops.mu.Unlock()
	var out []port.BankRef
	for b, deleting := range o.ops.deleting {
		if !deleting {
			continue
		}
		active := false
		for _, r := range o.ops.rows {
			if r.job.Bank == b && r.job.Kind == domain.OperationKindDeleteBank &&
				(r.status == domain.OperationStatusPending || r.status == domain.OperationStatusProcessing) {
				active = true
			}
		}
		if !active {
			out = append(out, b)
		}
	}
	return out, nil
}

// graphBanks is a GraphWorkSource returning fixed banks.
type graphBanks []port.BankRef

func (g graphBanks) BanksWithGraphWork(context.Context) ([]port.BankRef, error) { return g, nil }

func ptrBool(v bool) *bool { return &v }

// Accept (WP-20 item 1): the tick consolidates only banks whose effective config leaves automatic
// consolidation and observations on; a bank whose config can't be found (gone) is skipped silently.
func TestConsolidationReconcileSkipsDisabledBanks(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	bankD := port.BankRef{Tenant: "t3", Bank: "gone"}
	deps := MaintenanceDeps{
		Claimer: ops, Store: &fakeStore{ops: ops}, Clock: clock, Logger: quiet,
		Consolidation: &cutoffBanks{banks: []port.BankRef{bankA, bankB, bankC, bankD}},
		Config: bankConfigs{
			bankA: {AutoConsolidation: ptrBool(true), EnableObservations: ptrBool(true)},
			bankB: {AutoConsolidation: ptrBool(false), EnableObservations: ptrBool(true)},
			bankC: {AutoConsolidation: ptrBool(true), EnableObservations: ptrBool(false)},
		},
	}.withDefaults()
	n, err := reconcileConsolidation(context.Background(), deps)
	if err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v; want 1 inserted", n, err)
	}
	for b, want := range map[port.BankRef]int{bankA: 1, bankB: 0, bankC: 0, bankD: 0} {
		got := ops.count(func(r *fakeRow) bool { return r.job.Bank == b && r.job.Kind == domain.OperationKindConsolidate })
		if got != want {
			t.Errorf("bank %v: %d consolidate jobs, want %d", b, got, want)
		}
	}
}

// Accept (WP-20 item 1): the tick asks only for units older than the grace (default 10 min, configurable),
// so a skip_consolidation retain followed by a manual Consolidate within the grace is not pre-empted.
func TestConsolidationReconcileGrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grace time.Duration
		want  time.Duration
	}{
		{"default", 0, DefaultConsolidationGrace},
		{"configured", 3 * time.Minute, 3 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			ops := newOps(clock)
			src := &cutoffBanks{}
			deps := MaintenanceDeps{
				Claimer: ops, Store: &fakeStore{ops: ops}, Clock: clock, Logger: quiet, Consolidation: src,
				ConsolidationGrace: tc.grace,
			}.withDefaults()
			if _, err := reconcileConsolidation(context.Background(), deps); err != nil {
				t.Fatal(err)
			}
			if len(src.before) != 1 || !src.before[0].Equal(clock.Now().Add(-tc.want)) {
				t.Fatalf("cut-off %v, want now - %v", src.before, tc.want)
			}
		})
	}
	if DefaultConsolidationGrace != 10*time.Minute {
		t.Errorf("default grace = %v, want 10m", DefaultConsolidationGrace)
	}
}

// Accept (WP-20 item 2): a delete_bank that ReclaimStale fails at its attempt cap is re-enqueued by the
// same tick's resume step, and later ticks join that row instead of adding more.
func TestMaintenanceResumesStalledBankDelete(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	ops.deleting[bankC] = true
	limit := domain.OperationKindDeleteBank.MaxAttempts()
	if limit <= 0 {
		t.Fatalf("delete_bank is uncapped (%d); the test needs the cap", limit)
	}
	first := ops.add(port.Job{
		Bank: bankC, Kind: domain.OperationKindDeleteBank, SerialKey: bank.DeleteBankSerialKey,
		Attempt: limit - 1, Dedupe: port.DedupePendingOrRunning,
	})
	if js, _ := ops.Claim(context.Background(), "dead", []port.JobKind{domain.OperationKindDeleteBank}, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	clock.Advance(DefaultStaleAfter + time.Second)
	deps := MaintenanceDeps{
		Claimer: ops, Store: &fakeStore{ops: ops}, Clock: clock, Logger: quiet, Deletes: opsDeletes{ops},
	}.withDefaults()
	for range 3 {
		if err := maintenanceTick(context.Background(), deps); err != nil {
			t.Fatal(err)
		}
	}
	if row := ops.row(first); row.status != domain.OperationStatusFailed {
		t.Fatalf("reclaimed delete_bank at its cap: %s, want failed", row.status)
	}
	pending := ops.count(func(r *fakeRow) bool {
		return r.job.Bank == bankC && r.job.Kind == domain.OperationKindDeleteBank &&
			r.status == domain.OperationStatusPending && r.job.SerialKey == bank.DeleteBankSerialKey
	})
	if pending != 1 {
		t.Fatalf("%d pending delete_bank rows after three ticks, want exactly 1", pending)
	}
	js, _ := ops.Claim(context.Background(), "w", []port.JobKind{domain.OperationKindDeleteBank}, 1)
	if len(js) != 1 || js[0].Attempt != 0 {
		t.Fatalf("resumed delete_bank not claimable as a fresh job: %+v", js)
	}
}

// Accept (WP-20 item 3): the tick enqueues graph_maintenance for banks with graph work, joins a queued or
// running one, and skips a deleting bank.
func TestMaintenanceEnqueuesGraphMaintenance(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	ops.deleting[bankC] = true
	running := ops.add(port.Job{
		Bank: bankB, Kind: domain.OperationKindGraphMaintenance, SerialKey: graphSerialKey,
		Dedupe: port.DedupePendingOrRunning,
	})
	graphKinds := []port.JobKind{domain.OperationKindGraphMaintenance}
	if js, _ := ops.Claim(context.Background(), "w", graphKinds, 1); len(js) != 1 {
		t.Fatal("setup claim")
	}
	deps := MaintenanceDeps{
		Claimer: ops, Store: &fakeStore{ops: ops}, Clock: clock, Logger: quiet,
		Graph: graphBanks{bankA, bankB, bankC},
	}.withDefaults()
	n, err := reconcileGraph(context.Background(), deps)
	if err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v; want 1 inserted (bank A)", n, err)
	}
	if n, err := reconcileGraph(context.Background(), deps); err != nil || n != 0 {
		t.Fatalf("second reconcile = %d, %v; want 0 (joined)", n, err)
	}
	for b, want := range map[port.BankRef]int{bankA: 1, bankB: 1, bankC: 0} {
		got := ops.count(func(r *fakeRow) bool {
			return r.job.Bank == b && r.job.Kind == domain.OperationKindGraphMaintenance && r.job.SerialKey == graphSerialKey
		})
		if got != want {
			t.Errorf("bank %v: %d graph_maintenance rows, want %d", b, got, want)
		}
	}
	if row := ops.row(running); row.status != domain.OperationStatusProcessing {
		t.Errorf("running graph job: %s", row.status)
	}
}
