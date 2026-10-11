package postgres_test

import (
	"errors"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"reflect"
	"testing"
	"time"
)

// errNoRetry fails a job for good.
var errNoRetry = errors.New("delete failed")

// mtEmbedded returns a world fact of b with an embedding, created at created.
func mtEmbedded(b port.BankRef, text string, created time.Time) domain.Unit {
	u := fuUnit(b, text)
	u.Embedding = make([]float32, domain.EmbeddingDim)
	u.Embedding[0] = 1
	u.CreatedAt = created
	return u
}

// mtEnsure creates banks bs.
func mtEnsure(t *testing.T, s *postgres.Store, bs ...port.BankRef) {
	t.Helper()
	if err := fuTx(t, s, func(tx port.Tx) error {
		for _, b := range bs {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestUnconsolidatedBanks checks the consolidation reconcile's read: only units created before the cut-off
// count, consolidated units and tombstoned banks never do.
func TestUnconsolidatedBanks(t *testing.T) {
	s := postgres.New(pgtest.Fresh(t))
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	old, fresh := now.Add(-time.Hour), now.Add(-time.Minute)
	mtEnsure(t, s, fuBank, fuOther)
	fuInsert(t, s, mtEmbedded(fuBank, "old fact", old))
	fuInsert(t, s, mtEmbedded(fuOther, "fresh fact", fresh))
	stamped := mtEmbedded(fuOther, "old but consolidated", old)
	stamped.ConsolidatedAt = &now
	fuInsert(t, s, stamped)

	got, err := s.UnconsolidatedBanks(ctx, now.Add(-10*time.Minute))
	if want := []port.BankRef{fuBank}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("before the grace cut-off: %v %v, want %v", got, err, want)
	}
	got, err = s.UnconsolidatedBanks(ctx, now)
	if want := []port.BankRef{fuBank, fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cut-off now: %v %v, want %v", got, err, want)
	}
	fuMarkDeleting(t, s, fuBank)
	got, err = s.UnconsolidatedBanks(ctx, now)
	if want := []port.BankRef{fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstoned bank listed: %v %v, want %v", got, err, want)
	}
}

// TestStalledBankDeletes checks the delete-resume read: a tombstoned bank is listed only while it has no
// pending or processing delete_bank.
func TestStalledBankDeletes(t *testing.T) {
	s := postgres.New(pgtest.Fresh(t))
	ctx := t.Context()
	mtEnsure(t, s, fuBank, fuOther)
	if got, err := s.StalledBankDeletes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("live banks listed: %v %v", got, err)
	}
	fuMarkDeleting(t, s, fuBank)
	if got, err := s.StalledBankDeletes(ctx); err != nil || !reflect.DeepEqual(got, []port.BankRef{fuBank}) {
		t.Fatalf("tombstoned bank without a delete job: %v %v", got, err)
	}
	enqueueDelete := func() {
		t.Helper()
		if err := fuTx(t, s, func(tx port.Tx) error {
			_, _, err := tx.Queue().Enqueue(ctx, port.Job{
				Bank: fuBank, Kind: domain.OperationKindDeleteBank, SerialKey: "bank", Payload: []byte(`{}`),
				Dedupe: port.DedupePendingOrRunning,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	enqueueDelete()
	if got, _ := s.StalledBankDeletes(ctx); len(got) != 0 {
		t.Fatalf("listed with a pending delete_bank: %v", got)
	}
	jobs, err := s.Claim(ctx, "w", []port.JobKind{domain.OperationKindDeleteBank}, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	if got, _ := s.StalledBankDeletes(ctx); len(got) != 0 {
		t.Fatalf("listed with a processing delete_bank: %v", got)
	}
	if err := s.Fail(ctx, jobs[0].ID, errNoRetry, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StalledBankDeletes(ctx); !reflect.DeepEqual(got, []port.BankRef{fuBank}) {
		t.Fatalf("failed delete_bank not listed: %v", got)
	}
}

// TestBanksWithGraphWork checks the graph-maintenance reconcile's read: banks with graph_dirty or
// entity_maintenance rows, never tombstoned ones.
func TestBanksWithGraphWork(t *testing.T) {
	pool := pgtest.Fresh(t)
	s := postgres.New(pool)
	ctx := t.Context()
	third := port.BankRef{Tenant: "tenant-fu-3", Bank: "bank-fu"}
	mtEnsure(t, s, fuBank, fuOther, third)
	if got, err := s.BanksWithGraphWork(ctx); err != nil || len(got) != 0 {
		t.Fatalf("no graph work: %v %v", got, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO graph_dirty (tenant_id, bank_id, unit_id) VALUES ($1, $2, gen_random_uuid())`,
		fuBank.Tenant, fuBank.Bank); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO entity_maintenance (tenant_id, bank_id, entity_id) VALUES ($1, $2, gen_random_uuid())`,
		fuOther.Tenant, fuOther.Bank); err != nil {
		t.Fatal(err)
	}
	got, err := s.BanksWithGraphWork(ctx)
	if want := []port.BankRef{fuBank, fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("graph work: %v %v, want %v", got, err, want)
	}
	fuMarkDeleting(t, s, fuBank)
	got, err = s.BanksWithGraphWork(ctx)
	if want := []port.BankRef{fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstoned bank listed: %v %v, want %v", got, err, want)
	}
}
