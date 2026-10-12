package org

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// env is one test's database, fake blob store and Service over them.
type env struct {
	ctx  context.Context
	pool *pgxpool.Pool
	db   *postgres.DB
	blob *fake.Blob
	svc  *Service
	org  domain.OrgRef
}

// newEnv creates org "o" of tenant "t" with no bank, and puts n blobs under its prefix (blobs a crash left behind
// a deleted bank).
func newEnv(t *testing.T, blobs int) env {
	t.Helper()
	pool := pgtest.Pool(t)
	e := env{
		ctx: t.Context(), pool: pool, db: postgres.New(pool), blob: fake.NewBlob(),
		org: domain.OrgRef{Tenant: "t", Org: "o"},
	}
	pgtest.CreateOrgs(t, e.db, domain.NewBankRef("t", "o", ""))
	e.svc = e.service(e.blob)
	for i := range blobs {
		key := blob.Key(domain.NewBankRef("t", "o", "gone"), domain.DocumentID(fmt.Sprintf("d%d", i)), "h")
		if err := e.blob.Put(e.ctx, key, strings.NewReader("x"), 1, "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// service returns a Service over e's database and bs, with small pages so a delete spans several.
func (e env) service(bs blob.Store) *Service {
	return New(Deps{
		DB: e.db, Maintenance: e.db, Blob: bs, Clock: clock.NewManual(time.Now()),
		Delete: DeleteConfig{BlobPageSize: 2, SweepBatch: 2},
	})
}

// exec runs SQL on the test database.
func (e env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// claim claims the org's delete_org job, as a worker does.
func (e env) claim(t *testing.T) *jobs.Run {
	t.Helper()
	js, err := e.db.Claim(e.ctx, "w", []domain.OperationKind{domain.OperationKindDeleteOrg}, 1)
	if err != nil || len(js) != 1 {
		t.Fatalf("claim delete_org: %v, %v", js, err)
	}
	return jobs.NewRun(e.db, js[0])
}

// receipt reads the org's latest deletion receipt.
func (e env) receipt(t *testing.T) domain.DeletionReceipt {
	t.Helper()
	rc, err := e.svc.GetDeletionReceipt(e.ctx, e.org)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

// wantGone fails unless the org row, every row carrying its org_id and every blob under its prefix are gone, and its
// receipt is COMPLETED with a clean verification.
func (e env) wantGone(t *testing.T) {
	t.Helper()
	var rows int64
	if err := e.db.InTx(e.ctx, func(tx store.Tx) error {
		_, err := tx.Orgs().Get(e.ctx, e.org)
		if !errors.Is(err, domain.ErrOrgNotFound) {
			return fmt.Errorf("org after the delete: %w", err)
		}
		_, rows, err = tx.Orgs().CountRows(e.ctx, e.org)
		return err
	}); err != nil || rows != 0 {
		t.Fatalf("after the delete: %d rows left, %v", rows, err)
	}
	if keys, _ := e.blob.List(e.ctx, blob.OrgPrefix(e.org), "", 10); len(keys) != 0 {
		t.Fatalf("blobs left under the org: %v", keys)
	}
	rc := e.receipt(t)
	v := rc.Verification
	if rc.Status != domain.ReceiptStatusCompleted || rc.CompletedAt == nil || v == nil || v.RowsRemaining != 0 ||
		v.BlobsRemaining != 0 || v.TablesChecked == 0 {
		t.Fatalf("receipt after the delete: %+v %+v", rc, v)
	}
}

// failingBlob fails every DeletePrefix after the first ok calls, like a worker that dies part-way through the blobs.
type failingBlob struct {
	*fake.Blob
	ok    int32
	calls atomic.Int32
}

func (f *failingBlob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if f.calls.Add(1) > f.ok {
		return 0, errors.New("worker died")
	}
	return f.Blob.DeletePrefix(ctx, prefix) //nolint:wrapcheck // test double
}

// deleteTwice calls DeleteOrg twice and checks that it starts an IN_PROGRESS receipt and an org-scoped delete_org,
// and that the second call joins them.
func (e env) deleteTwice(t *testing.T) (domain.Operation, domain.DeletionReceipt) {
	t.Helper()
	op, rc, err := e.svc.DeleteOrg(e.ctx, e.org)
	if err != nil || rc.Status != domain.ReceiptStatusInProgress || op.Kind != domain.OperationKindDeleteOrg ||
		!op.Bank.OrgScoped() {
		t.Fatalf("DeleteOrg = %+v, %+v, %v", op, rc, err)
	}
	again, rc2, err := e.svc.DeleteOrg(e.ctx, e.org)
	if err != nil || again.ID != op.ID || rc2.ID != rc.ID {
		t.Fatalf("DeleteOrg again = %s / %s, %v; want the same operation %s and receipt %s", again.ID, rc2.ID, err,
			op.ID, rc.ID)
	}
	return op, rc
}

// TestDeleteOrgResumesAfterCrash: a worker that dies part-way through an org delete (some blobs deleted, its row
// left PROCESSING) is reclaimed; at its attempt cap the reclaim fails it, the tick's resume step enqueues a new
// delete_org, and that run finishes the job: rows a dead bank left behind, every blob, the org row, and a COMPLETED
// receipt. Re-issuing DeleteOrg meanwhile joins the delete instead of starting another.
func TestDeleteOrgResumesAfterCrash(t *testing.T) {
	t.Parallel()
	e := newEnv(t, 5)
	e.exec(t, `INSERT INTO graph_dirty (tenant_id, org_id, bank_id, unit_id) VALUES ('t', 'o', 'gone', gen_random_uuid())`)
	op, rc := e.deleteTwice(t)
	dying := &failingBlob{Blob: e.blob, ok: 2}
	if err := e.service(dying).RunDeleteJob(e.ctx, e.claim(t)); err == nil {
		t.Fatal("the dying run returned nil")
	}
	if keys := e.blob.Keys(); len(keys) != 3 {
		t.Fatalf("%d blobs left after the dying run, want 3 (2 deleted)", len(keys))
	}
	e.exec(t, `UPDATE operations SET heartbeat_at = now() - interval '1 hour', attempts = $1 WHERE id = $2`,
		domain.DefaultMaxAttempts-1, op.ID)
	if n, err := e.db.ReclaimStale(e.ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("ReclaimStale = %d, %v", n, err)
	}
	if n, err := e.svc.ResumeStalledDeletes(e.ctx); err != nil || n != 1 {
		t.Fatalf("ResumeStalledDeletes = %d, %v; want one delete_org enqueued", n, err)
	}
	if err := e.svc.RunDeleteJob(e.ctx, e.claim(t)); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	e.wantGone(t)
	if got := e.receipt(t); got.ID != rc.ID {
		t.Fatalf("completed receipt %s, want the request's %s", got.ID, rc.ID)
	}
	if n, err := e.svc.ResumeStalledDeletes(e.ctx); err != nil || n != 0 {
		t.Fatalf("ResumeStalledDeletes after the delete = %d, %v; want nothing to resume", n, err)
	}
}

// TestDeleteOrgVerificationCatchesLeftover: the verification counts rows in every table carrying org_id as the schema
// lists them, so a row in a table no sweep step knows about fails it. The receipt says FAILED_VERIFICATION with the
// count, the org row stays, and the run fails so it is retried; once the row is gone the next run completes.
func TestDeleteOrgVerificationCatchesLeftover(t *testing.T) {
	t.Parallel()
	e := newEnv(t, 1)
	e.exec(t, `CREATE TABLE later (tenant_id text NOT NULL, org_id text NOT NULL);
		INSERT INTO later VALUES ('t', 'o'), ('t', 'other')`)
	if _, _, err := e.svc.DeleteOrg(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
	run := e.claim(t)
	err := e.svc.RunDeleteJob(e.ctx, run)
	if err == nil || !strings.Contains(err.Error(), "1 rows") {
		t.Fatalf("run with a planted row = %v, want a failed verification naming 1 row", err)
	}
	rc := e.receipt(t)
	if rc.Status != domain.ReceiptStatusFailedVerification || rc.Verification == nil ||
		rc.Verification.RowsRemaining != 1 || rc.CompletedAt != nil {
		t.Fatalf("receipt after the failed verification: %+v %+v", rc, rc.Verification)
	}
	if err := e.db.InTx(e.ctx, func(tx store.Tx) error {
		org, err := tx.Orgs().Get(e.ctx, e.org)
		if err == nil && !org.Deleting() {
			err = errors.New("org no longer tombstoned")
		}
		return err
	}); err != nil {
		t.Fatalf("org after the failed verification: %v", err)
	}
	e.exec(t, `DELETE FROM later WHERE org_id = 'o'`)
	if err := e.svc.RunDeleteJob(e.ctx, run); err != nil {
		t.Fatalf("run after the leftover is gone: %v", err)
	}
	e.wantGone(t)
}
