package maintenance

import (
	"context"
	"memd/internal/bank"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
	"time"
)

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
