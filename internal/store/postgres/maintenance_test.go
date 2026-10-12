package postgres_test

import (
	"encoding/json"
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

// strandedRederive enqueues a rederive_memory of u and returns its id.
func strandedRederive(t *testing.T, s *postgres.DB, u domain.Unit) domain.OperationID {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"unit_id": string(u.ID)})
	var id domain.OperationID
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), store.Job{
			Bank: u.Bank, Kind: domain.OperationKindRederiveMemory, SerialKey: "doc:" + string(u.DocumentID),
			Payload: payload, Dedupe: store.DedupeNone,
		})
		return err
	}); err != nil {
		t.Fatalf("enqueue rederive: %v", err)
	}
	return id
}

// TestStrandedFacts checks the maintenance read behind the NULL-embedding rederive reconcile: a fact whose
// rederive failed for good is listed once the retry delay has passed, and not while a rederive is pending,
// once it has an embedding, or when its bank is tombstoned.
func TestStrandedFacts(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	stranded := fuUnit(fuBank, "stranded fact")
	stranded.DocumentID = "doc-1"
	healthy := fuUnit(fuBank, "healthy fact")
	healthy.Embedding = make([]float32, domain.EmbeddingDim)
	healthy.Embedding[0] = 1
	if err := fuTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, fuBank); err != nil {
			return err
		}
		return tx.Documents().Upsert(ctx, domain.Document{Bank: fuBank, ID: "doc-1"})
	}); err != nil {
		t.Fatal(err)
	}
	fuInsert(t, s, stranded, healthy)
	strandedRederive(t, s, stranded)
	strandedRederive(t, s, healthy)
	jobs, err := s.Claim(ctx, "w", []domain.OperationKind{domain.OperationKindRederiveMemory}, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	for _, j := range jobs {
		if err := s.Fail(ctx, j.ID, errors.New("embedder down"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.StrandedFacts(ctx, time.Hour, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("within the retry delay: %v %v", got, err)
	}
	got, err = s.StrandedFacts(ctx, 0, 10)
	want := []domain.Unit{{ID: stranded.ID, Bank: fuBank, DocumentID: "doc-1"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("stranded: %+v %v, want %+v", got, err, want)
	}
	pending := strandedRederive(t, s, stranded)
	if got, _ := s.StrandedFacts(ctx, 0, 10); len(got) != 0 {
		t.Fatalf("listed while rederive %s is pending: %+v", pending, got)
	}
	fuMarkDeleting(t, s, fuBank)
	if got, _ := s.StrandedFacts(ctx, 0, 10); len(got) != 0 {
		t.Fatalf("listed on a tombstoned bank: %+v", got)
	}
}

// settleBatch enqueues a retain_batch parent with n retain_document children and returns their ids.
func settleBatch(t *testing.T, s *postgres.DB, n int) (domain.OperationID, []domain.OperationID) {
	t.Helper()
	var parent domain.OperationID
	kids := make([]domain.OperationID, n)
	if err := fuTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), fuBank); err != nil {
			return err
		}
		var err error
		parent, _, err = tx.Queue().Enqueue(t.Context(), store.Job{
			Bank: fuBank, Kind: domain.OperationKindRetainBatch, Payload: json.RawMessage(`{}`),
		})
		if err != nil {
			return err
		}
		for i := range kids {
			kids[i], _, err = tx.Queue().Enqueue(t.Context(), store.Job{
				Bank: fuBank, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + string(rune('a'+i)),
				Payload: json.RawMessage(`{}`), ParentID: parent,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return parent, kids
}

// settleStatus reads an operation's status.
func settleStatus(t *testing.T, s *postgres.DB, id domain.OperationID) domain.Operation {
	t.Helper()
	var op domain.Operation
	if err := fuTx(t, s, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(t.Context(), fuBank, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return op
}

// TestSettleRetainBatches checks a retain_batch parent stays PENDING while a child runs, then aggregates its
// children: COMPLETED when all completed, FAILED when one failed.
func TestSettleRetainBatches(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	parent, kids := settleBatch(t, s, 2)
	jobs, err := s.Claim(ctx, "w", []domain.OperationKind{domain.OperationKindRetainDocument}, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	if err := s.Complete(ctx, kids[0], nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 0 {
		t.Fatalf("settled %d with a child running: %v", n, err)
	}
	if err := s.Complete(ctx, kids[1], nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 1 {
		t.Fatalf("settled %d: %v", n, err)
	}
	if op := settleStatus(t, s, parent); op.Status != domain.OperationStatusCompleted {
		t.Fatalf("parent %s", op.Status)
	}
	if n, _ := s.SettleRetainBatches(ctx); n != 0 {
		t.Fatalf("settled again: %d", n)
	}
}

// TestSettleRetainBatchFailedChild checks a parent with a failed child ends FAILED with an error.
func TestSettleRetainBatchFailedChild(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	failedParent, kids2 := settleBatch(t, s, 1)
	if _, err := s.Claim(ctx, "w", []domain.OperationKind{domain.OperationKindRetainDocument}, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, kids2[0], errors.New("boom"), nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRetainBatches(ctx); err != nil || n != 1 {
		t.Fatalf("settled %d: %v", n, err)
	}
	if op := settleStatus(t, s, failedParent); op.Status != domain.OperationStatusFailed || op.Error == "" {
		t.Fatalf("parent with a failed child: %s %q", op.Status, op.Error)
	}
}
