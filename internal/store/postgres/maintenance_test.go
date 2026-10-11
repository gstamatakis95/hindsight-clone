package postgres_test

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"reflect"
	"testing"
	"time"
)

// errNoRetry fails a job for good.
var errNoRetry = errors.New("delete failed")

// mtEmbeddedAt returns a world fact of b with an embedding, created at created.
func mtEmbeddedAt(b domain.BankRef, text string, created time.Time) domain.Unit {
	u := fuUnit(b, text)
	u.Embedding = make([]float32, domain.EmbeddingDim)
	u.Embedding[0] = 1
	u.CreatedAt = created
	return u
}

// mtEnsure creates banks bs.
func mtEnsure(t *testing.T, s *postgres.DB, bs ...domain.BankRef) {
	t.Helper()
	if err := fuTx(t, s, func(tx store.Tx) error {
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
// count, consolidated units and tombstoned banks never do, and each bank comes with its oldest waiting fact.
func TestUnconsolidatedBanks(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	old, fresh := now.Add(-time.Hour), now.Add(-time.Minute)
	mtEnsure(t, s, fuBank, fuOther)
	fuInsert(t, s, mtEmbeddedAt(fuBank, "old fact", old))
	fuInsert(t, s, mtEmbeddedAt(fuOther, "fresh fact", fresh))
	stamped := mtEmbeddedAt(fuOther, "old but consolidated", old)
	stamped.ConsolidatedAt = &now
	fuInsert(t, s, stamped)

	list := func(before time.Time) []string {
		t.Helper()
		got, err := s.UnconsolidatedBanks(ctx, before)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, b := range got {
			out = append(out, b.Bank.Bank+"@"+b.Oldest.UTC().Format(time.RFC3339))
		}
		return out
	}
	at := func(b domain.BankRef, ts time.Time) string { return b.Bank + "@" + ts.Format(time.RFC3339) }
	if got, want := list(now.Add(-10*time.Minute)), []string{at(fuBank, old)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before the grace cut-off: %v, want %v", got, want)
	}
	if got, want := list(now), []string{at(fuBank, old), at(fuOther, fresh)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cut-off now: %v, want %v", got, want)
	}
	fuMarkDeleting(t, s, fuBank)
	if got, want := list(now), []string{at(fuOther, fresh)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstoned bank listed: %v, want %v", got, want)
	}
}

// TestStalledBankDeletes checks the delete-resume read: a tombstoned bank is listed only while it has no
// pending or processing delete_bank.
func TestStalledBankDeletes(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	mtEnsure(t, s, fuBank, fuOther)
	if got, err := s.StalledBankDeletes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("live banks listed: %v %v", got, err)
	}
	fuMarkDeleting(t, s, fuBank)
	if got, err := s.StalledBankDeletes(ctx); err != nil || !reflect.DeepEqual(got, []domain.BankRef{fuBank}) {
		t.Fatalf("tombstoned bank without a delete job: %v %v", got, err)
	}
	if err := fuTx(t, s, func(tx store.Tx) error {
		_, _, err := tx.Queue().Enqueue(ctx, store.Job{
			Bank: fuBank, Kind: domain.OperationKindDeleteBank, SerialKey: "bank", Payload: []byte(`{}`),
			Dedupe: store.DedupePendingOrRunning,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StalledBankDeletes(ctx); len(got) != 0 {
		t.Fatalf("listed with a pending delete_bank: %v", got)
	}
	jobs, err := s.Claim(ctx, "w", []domain.OperationKind{domain.OperationKindDeleteBank}, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	if got, _ := s.StalledBankDeletes(ctx); len(got) != 0 {
		t.Fatalf("listed with a processing delete_bank: %v", got)
	}
	if err := s.Fail(ctx, jobs[0].ID, errNoRetry, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StalledBankDeletes(ctx); !reflect.DeepEqual(got, []domain.BankRef{fuBank}) {
		t.Fatalf("failed delete_bank not listed: %v", got)
	}
}

// TestBanksWithGraphWork checks the graph-maintenance reconcile's read: banks with graph_dirty or
// entity_maintenance rows, never tombstoned ones.
func TestBanksWithGraphWork(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	third := domain.BankRef{Tenant: "tenant-fu-3", Bank: "bank-fu"}
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
	if want := []domain.BankRef{fuBank, fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("graph work: %v %v, want %v", got, err, want)
	}
	fuMarkDeleting(t, s, fuBank)
	got, err = s.BanksWithGraphWork(ctx)
	if want := []domain.BankRef{fuOther}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstoned bank listed: %v %v, want %v", got, err, want)
	}
}
