package postgres_test

import (
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"
)

// orgSchemaTables lists the base tables with an org_id column, as the org delete's verification reads them.
func orgSchemaTables(t *testing.T) []string {
	t.Helper()
	rows, err := pgtest.Pool(t).Query(t.Context(), `
		SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'org_id' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

// TestOrgSweepCoversSchema: the org sweep's hand-written table list, plus the tables its other steps empty (banks by
// the bank deletes, operations and lifecycle_events by OrgRepo.Delete), is exactly the set of tables carrying
// org_id. A table added later fails here before its rows could fail an org delete's verification.
func TestOrgSweepCoversSchema(t *testing.T) {
	t.Parallel()
	got := append(postgres.OrgSweepTables(), "banks", "operations", "lifecycle_events")
	wantSet(t, "swept tables", got, orgSchemaTables(t))
}

// orgFixture is an org with two banks holding content, plus a bank with the same id in another org of the tenant
// that every org-delete statement must leave alone.
type orgFixture struct {
	s               *postgres.DB
	org             domain.OrgRef
	a, b, bystander domain.BankRef
}

// newOrgFixture creates the orgs and banks and writes units, documents and a pending job into each bank.
func newOrgFixture(t *testing.T) orgFixture {
	t.Helper()
	s := pgtest.NewStore(t)
	f := orgFixture{
		s: s, org: domain.OrgRef{Tenant: "t", Org: "gone"},
		a: domain.NewBankRef("t", "gone", "a"), b: domain.NewBankRef("t", "gone", "b"),
		bystander: domain.NewBankRef("t", "kept", "a"),
	}
	ensureBanks(t, s, f.a, f.b, f.bystander)
	for _, b := range []domain.BankRef{f.a, f.b, f.bystander} {
		insertUnits(t, s, fact(b, "x"), fact(b, "y"))
		upsertDocs(t, s, b, "d1")
		enqueueJob(t, s, store.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Payload: json.RawMessage(`{}`),
		})
	}
	return f
}

// countRows is OrgRepo.CountRows of o in its own transaction.
func countRows(t *testing.T, s store.DB, o domain.OrgRef) (int, int64) {
	t.Helper()
	var (
		tables int
		rows   int64
	)
	inTx(t, s, func(tx store.Tx) error {
		var err error
		tables, rows, err = tx.Orgs().CountRows(t.Context(), o)
		return err
	})
	return tables, rows
}

// TestOrgDeleteStatements walks the org delete's store calls: MarkDeleting tombstones the org and its banks,
// CancelOrg spares the deletes, org-scoped enqueues obey the tombstone, DeleteBatch empties every content table of
// the org (never another org's bank with the same id), Delete refuses while a bank row is left and then removes
// the operations, events and org row, and CountRows reaches 0.
func TestOrgDeleteStatements(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	s, ctx := f.s, t.Context()
	var banks, cancelled int
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Orgs().LockForDelete(ctx, f.org); err != nil {
			return err
		}
		var err error
		if banks, err = tx.Orgs().MarkDeleting(ctx, f.org, time.Now()); err != nil {
			return err
		}
		cancelled, err = tx.Queue().CancelOrg(ctx, f.org)
		return err
	})
	if banks != 2 || cancelled != 2 {
		t.Fatalf("MarkDeleting tombstoned %d banks and CancelOrg cancelled %d jobs, want 2 and 2", banks, cancelled)
	}
	enqueue := func(j store.Job) error { return enqueueErr(t, s, j) }
	orgJob := store.Job{
		Bank: f.org.Scope(), Kind: domain.OperationKindDeleteOrg, SerialKey: "org",
		Payload: json.RawMessage(`{}`),
	}
	wantErrIs(t, "org-scoped consolidate on a tombstoned org", enqueue(store.Job{
		Bank: f.org.Scope(), Kind: domain.OperationKindConsolidate, Payload: json.RawMessage(`{}`),
	}), domain.ErrOrgDeleting)
	missing := orgJob
	missing.Bank = domain.OrgRef{Tenant: "t", Org: "never"}.Scope()
	wantErrIs(t, "delete_org on an unknown org", enqueue(missing), domain.ErrOrgNotFound)
	if err := enqueue(orgJob); err != nil {
		t.Fatalf("delete_org on its tombstoned org: %v", err)
	}
	f.sweep(t)
	if _, rows := countRows(t, s, f.org); rows != 2+3 { // the two bank rows and three operations remain
		t.Fatalf("after the sweep CountRows = %d, want 5 (2 banks, 3 operations)", rows)
	}
	wantErrIs(t, "Delete with banks left", txErr(t, s, func(tx store.Tx) error {
		return tx.Orgs().Delete(ctx, f.org)
	}), domain.ErrFailedPrecondition)
	if _, err := pgtest.Pool(t).Exec(ctx, `DELETE FROM banks WHERE tenant_id = 't' AND org_id = 'gone'`); err != nil {
		t.Fatal(err)
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Orgs().Delete(ctx, f.org) })
	if tables, rows := countRows(t, s, f.org); rows != 0 || tables != len(orgSchemaTables(t)) {
		t.Errorf("after Delete CountRows = %d rows in %d tables, want 0 in every table with org_id", rows, tables)
	}
	if _, rows := countRows(t, s, f.bystander.OrgRef()); rows == 0 {
		t.Error("the other org's rows went with the delete")
	}
	wantErrIs(t, "Get of the deleted org", txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Orgs().Get(ctx, f.org)
		return err
	}), domain.ErrOrgNotFound)
}

// enqueueErr enqueues j in its own transaction and returns the error.
func enqueueErr(t *testing.T, s store.DB, j store.Job) error {
	t.Helper()
	return txErr(t, s, func(tx store.Tx) error { _, _, err := tx.Queue().Enqueue(t.Context(), j); return err })
}

// sweep runs DeleteBatch with a small limit until it deletes nothing.
func (f orgFixture) sweep(t *testing.T) {
	t.Helper()
	for range 100 {
		var n int
		inTx(t, f.s, func(tx store.Tx) error {
			var err error
			n, err = tx.Orgs().DeleteBatch(t.Context(), f.org, 3)
			return err
		})
		if n == 0 {
			return
		}
	}
	t.Fatal("DeleteBatch never reached 0")
}

// TestCountRowsSeesNewTables: the verification lists its tables from the schema, so a table added without a sweep
// step still has its org rows counted.
func TestCountRowsSeesNewTables(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	o := domain.OrgRef{Tenant: "t", Org: "o"}
	pgtest.CreateOrgs(t, s, domain.NewBankRef("t", "o", ""))
	tables, rows := countRows(t, s, o)
	if rows != 0 {
		t.Fatalf("empty org: CountRows = %d", rows)
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE later (tenant_id text, org_id text);
		INSERT INTO later VALUES ('t', 'o'), ('t', 'other')`); err != nil {
		t.Fatal(err)
	}
	if got, rows := countRows(t, s, o); got != tables+1 || rows != 1 {
		t.Errorf("CountRows = %d rows in %d tables, want 1 row in %d", rows, got, tables+1)
	}
}

// TestBankCreatedDuringOrgDelete: bank creation and the org delete's tombstoning serialize on the org row in both
// orders. A bank whose creation committed while the delete waited for the row lock is tombstoned with the others;
// a creation that waits for the delete fails with ErrOrgDeleting. No bank survives either way.
func TestBankCreatedDuringOrgDelete(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	o := domain.OrgRef{Tenant: "t", Org: "o"}
	pgtest.EnsureBanks(t, s, domain.NewBankRef("t", "o", "old"))
	created, release := make(chan struct{}), make(chan struct{})
	creation := make(chan error, 1)
	go func() {
		creation <- s.InTx(ctx, func(tx store.Tx) error {
			if _, err := tx.Banks().Ensure(ctx, domain.NewBankRef("t", "o", "racing")); err != nil {
				return err
			}
			close(created)
			<-release
			return nil
		})
	}()
	waitFor(t, "the racing creation", created)
	deleted := make(chan int, 1)
	go func() {
		var n int
		err := s.InTx(ctx, func(tx store.Tx) error {
			if _, err := tx.Orgs().LockForDelete(ctx, o); err != nil {
				return err
			}
			var err error
			n, err = tx.Orgs().MarkDeleting(ctx, o, time.Now())
			return err
		})
		if err != nil {
			n = -1
		}
		deleted <- n
	}()
	select {
	case n := <-deleted:
		t.Fatalf("the org delete finished (%d banks) while a bank creation held the org row", n)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-creation; err != nil {
		t.Fatalf("racing creation: %v", err)
	}
	if n := waitFor(t, "the org delete", deleted); n != 2 {
		t.Fatalf("the org delete tombstoned %d banks, want 2 (the racing one included)", n)
	}
	err := ensureOnly(t, s, domain.NewBankRef("t", "o", "after"))
	wantErrIs(t, "a creation after the delete", err, domain.ErrOrgDeleting)
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM banks WHERE tenant_id = 't' AND org_id = 'o'
		AND deleting_at IS NULL`).Scan(&live); err != nil || live != 0 {
		t.Errorf("%d live banks left in the deleting org (%v)", live, err)
	}
}

// TestOrgScopedJobLifecycle: a delete_org job is claimed and stays alive while its org is tombstoned (a bank job
// of that org is not claimed), it is reclaimed and failed without the per-bank failure event, and a FAILED one can
// be retried on its tombstoned org.
func TestOrgScopedJobLifecycle(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	o := domain.OrgRef{Tenant: "t", Org: "o"}
	b := domain.NewBankRef("t", "o", "b")
	ensureQueueBanks(t, s, b)
	enqueueJob(t, s, store.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate",
		Payload: json.RawMessage(`{}`),
	})
	markOrgDeleting(t, pool, o)
	id := enqueueJob(t, s, store.Job{
		Bank: o.Scope(), Kind: domain.OperationKindDeleteOrg, SerialKey: "org",
		Payload: json.RawMessage(`{}`),
	})
	claimOnlyOrgJob(t, s, id)
	if _, err := pool.Exec(ctx, `UPDATE operations SET heartbeat_at = now() - interval '1 hour',
		attempts = $2 WHERE id = $1`, id, domain.DefaultMaxAttempts-1); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReclaimStale(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("ReclaimStale = %d, %v; want the delete_org failed", n, err)
	}
	op, err := getOp(t, s, o.Scope(), id)
	if err != nil || op.Status != domain.OperationStatusFailed {
		t.Fatalf("delete_org after reclaim at its cap: %+v, %v; want FAILED", op, err)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE org_id = 'o'`).Scan(&events); err != nil ||
		events != 0 {
		t.Errorf("%d lifecycle events written for an org-scoped failure (%v)", events, err)
	}
	err = txErr(t, s, func(tx store.Tx) error { _, err := tx.Queue().Retry(ctx, o.Scope(), id); return err })
	if err != nil {
		t.Errorf("Retry(delete_org) on its tombstoned org: %v", err)
	}
	if busy := processingInOrg(t, s, o, ""); busy {
		t.Error("ProcessingInOrg after the retry: want nothing processing")
	}
}

// claimOnlyOrgJob claims under a tombstoned org and checks that only delete_org id comes back, and alive.
func claimOnlyOrgJob(t *testing.T, s *postgres.DB, id domain.OperationID) {
	t.Helper()
	kinds := []domain.OperationKind{domain.OperationKindConsolidate, domain.OperationKindDeleteOrg}
	js, err := s.Claim(t.Context(), "w", kinds, 10)
	if err != nil || len(js) != 1 || js[0].ID != id || !js[0].Bank.OrgScoped() {
		t.Fatalf("Claim under a tombstoned org = %+v, %v; want only the delete_org", js, err)
	}
	if alive, err := s.Alive(t.Context(), id); err != nil || !alive {
		t.Fatalf("Alive(delete_org) = %v, %v; want true while its org row exists", alive, err)
	}
}

// processingInOrg is Queue.ProcessingInOrg in its own transaction.
func processingInOrg(t *testing.T, s store.DB, o domain.OrgRef, except domain.OperationID) bool {
	t.Helper()
	var busy bool
	inTx(t, s, func(tx store.Tx) error {
		var err error
		busy, err = tx.Queue().ProcessingInOrg(t.Context(), o, except)
		return err
	})
	return busy
}

// TestReceipts: Latest finds the newest receipt of an org by its id hash, never another org's, and Prune drops only
// receipts completed before the cutoff.
func TestReceipts(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	o := domain.OrgRef{Tenant: "t", Org: "o"}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var first, second string
	inTx(t, s, func(tx store.Tx) error {
		var err error
		rc := domain.DeletionReceipt{
			Tenant: "t", OrgIDHash: domain.OrgIDHash(o), Status: domain.ReceiptStatusInProgress,
			RequestedAt: at,
		}
		if first, err = tx.Receipts().Create(ctx, rc); err != nil {
			return err
		}
		rc.RequestedAt = at.Add(time.Hour)
		second, err = tx.Receipts().Create(ctx, rc)
		return err
	})
	got := latestReceipt(t, s, o)
	if got.ID != second || got.OrgIDHash == "o" || got.Verification != nil {
		t.Fatalf("Latest = %+v, want receipt %s without a verification", got, second)
	}
	wantErrIs(t, "Latest of another org", txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Receipts().Latest(ctx, domain.OrgRef{Tenant: "u", Org: "o"})
		return err
	}), domain.ErrNotFound)
	done := got
	done.Status, done.CompletedAt = domain.ReceiptStatusCompleted, &at
	done.Verification = &domain.ReceiptVerification{TablesChecked: 20, VerifiedAt: at}
	inTx(t, s, func(tx store.Tx) error { return tx.Receipts().Update(ctx, done) })
	if got := latestReceipt(t, s, o); got.Verification == nil || got.Verification.TablesChecked != 20 {
		t.Fatalf("after Update: %+v", got)
	}
	var pruned int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		pruned, err = tx.Receipts().Prune(ctx, at.Add(time.Second))
		return err
	})
	if pruned != 1 {
		t.Fatalf("Prune = %d, want 1 (only the completed receipt)", pruned)
	}
	inTx(t, s, func(tx store.Tx) error {
		_, err := tx.Receipts().Get(ctx, "t", first)
		return err
	})
}

// latestReceipt is ReceiptRepo.Latest in its own transaction.
func latestReceipt(t *testing.T, s store.DB, o domain.OrgRef) domain.DeletionReceipt {
	t.Helper()
	var rc domain.DeletionReceipt
	inTx(t, s, func(tx store.Tx) error {
		var err error
		rc, err = tx.Receipts().Latest(t.Context(), o)
		return err
	})
	return rc
}
