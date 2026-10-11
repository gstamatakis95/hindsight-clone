package maintenance

import (
	"context"
	"maps"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
	"time"
)

// bankConfigs is a config.Resolver serving a fixed effective config per bank; unknown banks are
// domain.ErrNotFound.
type bankConfigs map[domain.BankRef]domain.BankConfig

func (c bankConfigs) Effective(_ context.Context, b domain.BankRef, _ ...domain.BankConfig) (domain.BankConfig, error) {
	cfg, ok := c[b]
	if !ok {
		return domain.BankConfig{}, domain.ErrNotFound
	}
	return cfg, nil
}

func (c bankConfigs) ForScope(context.Context, domain.ScopeKey, *domain.BankConfig) (domain.BankConfig, error) {
	panic("not used")
}

// waitingBanks is a store.Maintenance whose UnconsolidatedBanks returns fixed banks with their oldest waiting
// fact.
type waitingBanks struct {
	store.Maintenance
	banks []store.UnconsolidatedBank
}

func (w waitingBanks) UnconsolidatedBanks(context.Context, time.Time) ([]store.UnconsolidatedBank, error) {
	return w.banks, nil
}

// waiting lists banks whose oldest fact has waited since the zero time (longer than any grace).
func waiting(bs ...domain.BankRef) waitingBanks {
	var w waitingBanks
	for _, b := range bs {
		w.banks = append(w.banks, store.UnconsolidatedBank{Bank: b})
	}
	return w
}

// graphBanks is a store.Maintenance whose BanksWithGraphWork returns fixed banks.
type graphBanks struct {
	store.Maintenance
	banks []domain.BankRef
}

func (g graphBanks) BanksWithGraphWork(context.Context) ([]domain.BankRef, error) {
	return g.banks, nil
}

// The tick consolidates only banks whose effective config leaves automatic
// consolidation and observations on; a bank whose config can't be found (gone) is skipped silently.
func TestConsolidationReconcileSkipsDisabledBanks(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	bankD := domain.BankRef{Tenant: "t3", Bank: "gone"}
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Logger: quiet,
		Consolidation: waiting(bankA, bankB, bankC, bankD),
		Resolver: bankConfigs{
			bankA: observations(domain.ObservationConfig{AutoConsolidation: ptrBool(true), Enabled: ptrBool(true)}),
			bankB: observations(domain.ObservationConfig{AutoConsolidation: ptrBool(false), Enabled: ptrBool(true)}),
			bankC: observations(domain.ObservationConfig{AutoConsolidation: ptrBool(true), Enabled: ptrBool(false)}),
		},
	})
	n, err := deps.reconcileConsolidation(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v; want 1 inserted", n, err)
	}
	for b, want := range map[domain.BankRef]int{bankA: 1, bankB: 0, bankC: 0, bankD: 0} {
		if got := ops.countBank(b, domain.OperationKindConsolidate, ""); got != want {
			t.Errorf("bank %v: %d consolidate jobs, want %d", b, got, want)
		}
	}
}

// The tick consolidates a bank only once its oldest waiting fact is older than the bank's own
// observations.reconcile_grace_seconds (default 10 min), so a skip_consolidation retain followed by a manual
// Consolidate within the grace is not pre-empted.
func TestConsolidationReconcileGrace(t *testing.T) {
	clock := newClock()
	ops := newOps(t)
	fiveMinutesAgo := clock.Now().Add(-5 * time.Minute)
	src := waitingBanks{banks: []store.UnconsolidatedBank{
		{Bank: bankA, Oldest: fiveMinutesAgo},
		{Bank: bankB, Oldest: fiveMinutesAgo},
		{Bank: bankC, Oldest: fiveMinutesAgo},
	}}
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Logger: quiet, Consolidation: src,
		Resolver: bankConfigs{
			bankA: observations(domain.ObservationConfig{ReconcileGraceSeconds: ptrInt(180)}),
			bankB: {},
			bankC: observations(domain.ObservationConfig{ReconcileGraceSeconds: ptrInt(600)}),
		},
	})
	if _, err := deps.reconcileConsolidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	for b, want := range map[domain.BankRef]int{bankA: 1, bankB: 0, bankC: 0} {
		if got := ops.countBank(b, domain.OperationKindConsolidate, ""); got != want {
			t.Errorf("bank %v: %d consolidate jobs, want %d", b, got, want)
		}
	}
}

// observations is a config with only the observations group set.
func observations(o domain.ObservationConfig) domain.BankConfig {
	return domain.BankConfig{Observations: &o}
}

func ptrInt(v int) *int { return &v }

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
	deps := New(Deps{
		Claimer: ops, Store: ops, Clock: clock, Logger: quiet, Deletes: ops,
	})
	for range 3 {
		if err := deps.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if row := ops.row(first); row.status != domain.OperationStatusFailed {
		t.Fatalf("reclaimed delete_bank at its cap: %s, want failed", row.status)
	}
	pending := ops.countBank(bankC, domain.OperationKindDeleteBank, "status = 'pending' AND serial_key = $4",
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
		got := ops.countBank(b, domain.OperationKindGraphMaintenance, "serial_key = $4", jobs.SerialKeyGraph)
		if got != want {
			t.Errorf("bank %v: %d graph_maintenance rows, want %d", b, got, want)
		}
	}
	if row := ops.row(running); row.status != domain.OperationStatusProcessing {
		t.Errorf("running graph job: %s", row.status)
	}
}

// strandedSource is a store.Maintenance whose StrandedFacts returns fixed units.
type strandedSource struct {
	store.Maintenance
	units []domain.Unit
	after time.Duration
}

func (s *strandedSource) StrandedFacts(_ context.Context, retryAfter time.Duration, _ int) ([]domain.Unit, error) {
	s.after = retryAfter
	return s.units, nil
}

// The tick re-enqueues rederive_memory for facts a failed rederive left with a NULL embedding, with
// curation's payload and serial key, after the default retry delay.
func TestMaintenanceRederivesStrandedFacts(t *testing.T) {
	ops := newOps(t)
	src := &strandedSource{units: []domain.Unit{
		{ID: "u1", Bank: bankA, DocumentID: "d1"}, {ID: "u2", Bank: bankA},
	}}
	s := New(Deps{Claimer: ops, Store: ops, Clock: newClock(), Stranded: src, Logger: quiet})
	if n, err := s.rederiveStranded(t.Context()); err != nil || n != 2 {
		t.Fatalf("rederiveStranded = %d, %v; want 2", n, err)
	}
	claimed, err := ops.Claim(t.Context(), "w", []domain.OperationKind{domain.OperationKindRederiveMemory}, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range claimed {
		got[j.SerialKey] = string(j.Payload)
	}
	want := map[string]string{"doc:d1": `{"unit_id": "u1"}`, "unit:u2": `{"unit_id": "u2"}`}
	if !maps.Equal(got, want) {
		t.Fatalf("rederives %v, want %v", got, want)
	}
	if src.after != DefaultStrandedRetryAfter {
		t.Fatalf("retry-after %s", src.after)
	}
}
