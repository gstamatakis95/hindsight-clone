package main

import (
	"context"
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

// retainInto retains one short document into bank of org and returns the call's error.
func (w *wiredStack) retainInto(ctx context.Context, org, bank, text string) error {
	_, err := pb.NewMemoryServiceClient(w.conn).Retain(ctx, &pb.RetainRequest{
		OrgId: org, BankId: bank, Items: []*pb.RetainItem{{Content: text}},
	})
	return err //nolint:wrapcheck // test helper
}

// createOrgWithBanks creates org in tenant "t" and retains a document into each of banks.
func (w *wiredStack) createOrgWithBanks(t *testing.T, ctx context.Context, org string, banks ...string) {
	t.Helper()
	if _, err := pb.NewOrgServiceClient(w.conn).CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	for _, b := range banks {
		if err := w.retainInto(ctx, org, b, "Alice works as a nurse in "+b+"."); err != nil {
			t.Fatalf("retain into %s/%s: %v", org, b, err)
		}
	}
}

// orgLeft returns how many rows of tenant t's org carry its org_id (in every table that has one) and how many blobs
// lie under its prefix.
func (w *wiredStack) orgLeft(t *testing.T, org string) (int64, int) {
	t.Helper()
	o := domain.OrgRef{Tenant: "t", Org: org}
	var rows int64
	if err := w.db.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		_, rows, err = tx.Orgs().CountRows(t.Context(), o)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	keys, err := w.blob.List(t.Context(), blob.OrgPrefix(o), "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows, len(keys)
}

// waitReceipt polls GetDeletionReceipt for org until it is COMPLETED and returns it.
func (w *wiredStack) waitReceipt(t *testing.T, ctx context.Context, org string) *pb.DeletionReceipt {
	t.Helper()
	orgs := pb.NewOrgServiceClient(w.conn)
	for {
		resp, err := orgs.GetDeletionReceipt(ctx, &pb.GetDeletionReceiptRequest{OrgId: org})
		if err != nil {
			t.Fatalf("GetDeletionReceipt(%s): %v", org, err)
		}
		if rc := resp.GetReceipt(); rc.GetState() == pb.DeletionReceipt_STATE_COMPLETED {
			return rc
		}
		select {
		case <-ctx.Done():
			t.Fatalf("org %s delete not completed: %v", org, resp.GetReceipt())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestDeleteOrgWhileRunning: from the moment DeleteOrg returns, writes under the org fail FAILED_PRECONDITION
// (ORG_DELETING) and reads NOT_FOUND, GetOrg included; the org's delete_org operation is readable (org-scoped, no
// bank) and can't be cancelled; DeleteOrg again joins the running delete and returns the same receipt. A long blob
// grace keeps the delete from finishing, so every check sees it running.
func TestDeleteOrgWhileRunning(t *testing.T) {
	w := startWiredFile(t, nil, func(f *ServerFile) {
		grace := Duration(time.Hour)
		f.Deletion.BlobGrace = &grace
	})
	ctx := w.ctx(t)
	w.createOrgWithBanks(t, ctx, "del", "b1", "b2")
	orgs, ops := pb.NewOrgServiceClient(w.conn), pb.NewOperationServiceClient(w.conn)
	del, err := orgs.DeleteOrg(ctx, &pb.DeleteOrgRequest{OrgId: "del"})
	if err != nil {
		t.Fatal(err)
	}
	op, rc := del.GetOperation(), del.GetReceipt()
	if op.GetKind() != pb.OperationKind_OPERATION_KIND_DELETE_ORG || op.GetBankId() != "" || op.GetOrgId() != "del" ||
		rc.GetState() != pb.DeletionReceipt_STATE_IN_PROGRESS || rc.GetBanks() != 2 {
		t.Fatalf("DeleteOrg = %v, %v", op, rc)
	}
	wantStatus(t, "Retain into a bank of the deleting org", w.retainInto(ctx, "del", "b1", "x"),
		codes.FailedPrecondition, "ORG_DELETING")
	wantStatus(t, "Retain into a new bank of the deleting org", w.retainInto(ctx, "del", "new", "x"),
		codes.FailedPrecondition, "ORG_DELETING")
	_, err = pb.NewBankServiceClient(w.conn).CreateBank(ctx, &pb.CreateBankRequest{OrgId: "del", BankId: "new"})
	wantStatus(t, "CreateBank in the deleting org", err, codes.FailedPrecondition, "ORG_DELETING")
	w.wantWritesRefused(t, ctx)
	_, err = orgs.CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: "del"})
	wantStatus(t, "CreateOrg of the deleting org", err, codes.FailedPrecondition, "ORG_DELETING")
	_, err = orgs.GetOrg(ctx, &pb.GetOrgRequest{OrgId: "del"})
	wantStatus(t, "GetOrg of the deleting org", err, codes.NotFound, "ORG_NOT_FOUND")
	_, err = pb.NewBankServiceClient(w.conn).GetBank(ctx, &pb.GetBankRequest{OrgId: "del", BankId: "b1"})
	wantStatus(t, "GetBank in the deleting org", err, codes.NotFound, "")
	_, err = pb.NewMemoryServiceClient(w.conn).Recall(ctx, &pb.RecallRequest{OrgId: "del", BankId: "b1", Query: "x"})
	wantStatus(t, "Recall in the deleting org", err, codes.NotFound, "")
	got, err := ops.GetOperation(ctx, &pb.GetOperationRequest{OrgId: "del", OperationId: op.GetId()})
	if err != nil || got.GetOperation().GetKind() != pb.OperationKind_OPERATION_KIND_DELETE_ORG {
		t.Errorf("GetOperation of the org-scoped delete_org = %v, %v", got, err)
	}
	_, err = ops.CancelOperation(ctx, &pb.CancelOperationRequest{OrgId: "del", OperationId: op.GetId()})
	wantStatus(t, "CancelOperation of delete_org", err, codes.FailedPrecondition, "OPERATION_NOT_CANCELLABLE")
	again, err := orgs.DeleteOrg(ctx, &pb.DeleteOrgRequest{OrgId: "del"})
	if err != nil || again.GetOperation().GetKind() != pb.OperationKind_OPERATION_KIND_DELETE_ORG ||
		again.GetReceipt().GetId() != rc.GetId() {
		t.Errorf("DeleteOrg again = %v, %v; want the running delete_org and receipt %s", again, err, rc.GetId())
	}
}

// wantWritesRefused checks that the other write RPCs on a bank of the deleting org "del" fail FAILED_PRECONDITION
// (ORG_DELETING or BANK_DELETING), never NOT_FOUND.
func (w *wiredStack) wantWritesRefused(t *testing.T, ctx context.Context) {
	t.Helper()
	writes := map[string]func() error{
		"RetainBatch": func() error {
			_, err := pb.NewMemoryServiceClient(w.conn).RetainBatch(ctx, &pb.RetainBatchRequest{
				OrgId: "del", BankId: "b1", Items: []*pb.RetainItem{{Content: "x"}},
			})
			return err //nolint:wrapcheck // test
		},
		"Consolidate": func() error {
			_, err := pb.NewObservationServiceClient(w.conn).Consolidate(ctx, &pb.ConsolidateRequest{
				OrgId: "del", BankId: "b1",
			})
			return err //nolint:wrapcheck // test
		},
		"CreateMentalModel": func() error {
			_, err := pb.NewMentalModelServiceClient(w.conn).CreateMentalModel(ctx, &pb.CreateMentalModelRequest{
				OrgId: "del", BankId: "b1", Spec: &pb.MentalModelSpec{Id: "m", SourceQuery: "q"},
			})
			return err //nolint:wrapcheck // test
		},
		"CreateDirective": func() error {
			_, err := pb.NewConfigServiceClient(w.conn).CreateDirective(ctx, &pb.CreateDirectiveRequest{
				OrgId: "del", BankId: "b1", Directive: &pb.Directive{Name: "d", Rules: []string{"r"}},
			})
			return err //nolint:wrapcheck // test
		},
	}
	for name, call := range writes {
		if err := call(); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%s on a bank of the deleting org: %v, want FAILED_PRECONDITION", name, err)
		}
	}
}

// TestDeleteOrgEndToEnd: an org with three banks is deleted by the worker (delete_org fanning out to delete_bank):
// the receipt ends COMPLETED with a clean verification, no row carrying the org's id and no blob under its prefix is
// left, another org's bank with the same id is untouched, the org's operations are gone, and the org id can be
// created again, empty.
func TestDeleteOrgEndToEnd(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	w.createOrgWithBanks(t, ctx, "del", "b1", "b2", "b3")
	w.bank(t, "b1") // the same bank id in the test org
	if rows, blobs := w.orgLeft(t, "del"); rows == 0 || blobs != 3 {
		t.Fatalf("before the delete: %d rows, %d blobs", rows, blobs)
	}
	del, err := pb.NewOrgServiceClient(w.conn).DeleteOrg(ctx, &pb.DeleteOrgRequest{OrgId: "del"})
	if err != nil {
		t.Fatal(err)
	}
	wantCleanReceipt(t, w.waitReceipt(t, ctx, "del"), del.GetReceipt().GetId(), 3)
	if rows, blobs := w.orgLeft(t, "del"); rows != 0 || blobs != 0 {
		t.Fatalf("after the delete: %d rows, %d blobs left", rows, blobs)
	}
	if rows, blobs := w.orgLeft(t, testOrg); rows == 0 || blobs != 1 {
		t.Fatalf("the other org lost data: %d rows, %d blobs", rows, blobs)
	}
	_, err = pb.NewOperationServiceClient(w.conn).GetOperation(ctx, &pb.GetOperationRequest{
		OrgId: "del", OperationId: del.GetOperation().GetId(),
	})
	wantStatus(t, "GetOperation after the delete", err, codes.NotFound, "ORG_NOT_FOUND")
	if _, err := pb.NewOrgServiceClient(w.conn).CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: "del"}); err != nil {
		t.Fatalf("CreateOrg after the delete: %v", err)
	}
	banks, err := pb.NewBankServiceClient(w.conn).ListBanks(ctx, &pb.ListBanksRequest{OrgId: "del"})
	if err != nil || len(banks.GetBanks()) != 0 {
		t.Errorf("the recreated org lists %v, %v; want no bank", banks.GetBanks(), err)
	}
}

// wantCleanReceipt fails unless rc is receipt id, completed over banks banks with a clean verification.
func wantCleanReceipt(t *testing.T, rc *pb.DeletionReceipt, id string, banks int32) {
	t.Helper()
	if rc.GetBanks() != banks || rc.GetTablesChecked() == 0 || rc.GetRowsRemaining() != 0 || rc.GetBlobsRemaining() != 0 ||
		rc.GetCompletedAt() == nil || rc.GetId() != id {
		t.Fatalf("completed receipt = %v", rc)
	}
}

// TestRetainRacingDeleteOrg: retains into existing and new banks of an org keep running while the org is deleted.
// Each either commits before the delete (and its rows and blob are deleted with the org) or is refused (the org or
// bank is being deleted, or the org is gone); once the receipt is COMPLETED, no row and no blob of the org is left.
func TestRetainRacingDeleteOrg(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	w.createOrgWithBanks(t, ctx, "race", "seed")
	stop := make(chan struct{})
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
				}
				bank := []string{"seed", fmt.Sprintf("new-%d-%d", g, i)}[i%2]
				if err := w.retainInto(ctx, "race", bank, fmt.Sprintf("Fact %d from writer %d.", i, g)); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := pb.NewOrgServiceClient(w.conn).DeleteOrg(ctx, &pb.DeleteOrgRequest{OrgId: "race"}); err != nil {
		close(stop)
		t.Fatal(err)
	}
	w.waitReceipt(t, ctx, "race")
	close(stop)
	wg.Wait()
	for _, err := range errs {
		switch status.Code(err) {
		case codes.FailedPrecondition, codes.NotFound:
		default:
			t.Errorf("racing retain: %v, want success, FAILED_PRECONDITION or NOT_FOUND", err)
		}
	}
	if rows, blobs := w.orgLeft(t, "race"); rows != 0 || blobs != 0 {
		t.Fatalf("after the delete: %d rows, %d blobs left", rows, blobs)
	}
}
