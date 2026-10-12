package stub

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	memoryv1 "memd/gen/memory/v1"
)

func tenantCtx(tenant string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(TenantHeader, tenant))
}

func retain(t *testing.T, s *Server, ctx context.Context, bank string, items ...*memoryv1.RetainItem) {
	t.Helper()
	if _, err := s.CreateOrg(ctx, &memoryv1.CreateOrgRequest{
		OrgId: "o",
	}); err != nil && status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CreateOrg: %v", err)
	}
	if _, err := s.Retain(ctx, &memoryv1.RetainRequest{OrgId: "o", BankId: bank, Items: items}); err != nil {
		t.Fatalf("Retain: %v", err)
	}
}

func TestTenantRequired(t *testing.T) {
	s := New(Options{})
	for _, ctx := range []context.Context{context.Background(), tenantCtx("")} {
		_, err := s.Retain(ctx, &memoryv1.RetainRequest{
			OrgId: "o", BankId: "b", Items: []*memoryv1.RetainItem{{Content: "x"}},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Retain without tenant: got %v, want InvalidArgument", err)
		}
	}
}

func TestUnknownBankNotFound(t *testing.T) {
	s := New(Options{})
	ctx := tenantCtx("t1")
	retain(t, s, ctx, "b", &memoryv1.RetainItem{Content: "hello world"})
	other := tenantCtx("t2") // Banks are scoped per tenant.
	calls := map[string]func() error{
		"GetBankStats": func() error {
			_, err := s.GetBankStats(other, &memoryv1.GetBankStatsRequest{OrgId: "o", BankId: "b"})
			return err
		},
		"Recall": func() error {
			_, err := s.Recall(ctx, &memoryv1.RecallRequest{OrgId: "o", BankId: "missing", Query: "hello"})
			return err
		},
		"Reflect": func() error {
			_, err := s.Reflect(ctx, &memoryv1.ReflectRequest{OrgId: "o", BankId: "missing", Query: "hello"})
			return err
		},
		"ListOperations": func() error {
			_, err := s.ListOperations(ctx, &memoryv1.ListOperationsRequest{OrgId: "o", BankId: "missing"})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); status.Code(err) != codes.NotFound {
			t.Errorf("%s: got %v, want NotFound", name, err)
		}
	}
}

func TestPendingPollsDrain(t *testing.T) {
	s := New(Options{PendingPolls: 2})
	ctx := tenantCtx("t1")
	retain(t, s, ctx, "b", &memoryv1.RetainItem{Content: "a"})
	want := []int{1, 1, 0, 0}
	for i, w := range want {
		resp, err := s.GetBankStats(ctx, &memoryv1.GetBankStatsRequest{OrgId: "o", BankId: "b"})
		if err != nil {
			t.Fatalf("GetBankStats: %v", err)
		}
		if got := len(resp.GetStats().GetPendingOperationsByKind()); got != w {
			t.Fatalf("poll %d: %d pending kinds, want %d", i, got, w)
		}
	}
	retain(t, s, ctx, "b", &memoryv1.RetainItem{Content: "b"})
	resp, err := s.GetBankStats(ctx, &memoryv1.GetBankStatsRequest{OrgId: "o", BankId: "b"})
	if err != nil {
		t.Fatalf("GetBankStats: %v", err)
	}
	if resp.GetStats().GetPendingOperationsByKind()["consolidate"] != 1 {
		t.Fatalf("a new Retain should re-arm pending polls: %v", resp.GetStats())
	}
}

func TestRecallRanking(t *testing.T) {
	s := New(Options{})
	ctx := tenantCtx("t1")
	retain(t, s, ctx, "b",
		&memoryv1.RetainItem{DocumentId: "d1", Content: "user: I adopted a dog\nuser: the dog is named Rex"},
		&memoryv1.RetainItem{DocumentId: "d2", Content: "assistant: dogs are great\nuser: my dog Rex likes parks"},
	)
	resp, err := s.Recall(ctx, &memoryv1.RecallRequest{OrgId: "o", BankId: "b", Query: "What is my dog named?"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	var got []string
	for _, m := range resp.GetResults() {
		got = append(got, m.GetText())
	}
	want := []string{"user: the dog is named Rex", "user: I adopted a dog", "user: my dog Rex likes parks"}
	if len(got) != len(want) {
		t.Fatalf("Recall = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Recall = %q, want %q", got, want)
		}
	}

	// Upserting d1 replaces its lines.
	retain(t, s, ctx, "b", &memoryv1.RetainItem{DocumentId: "d1", Content: "user: I have a cat"})
	refl, err := s.Reflect(ctx, &memoryv1.ReflectRequest{OrgId: "o", BankId: "b", Query: "cat"})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if refl.GetText() != "user: I have a cat" {
		t.Fatalf("Reflect = %q", refl.GetText())
	}
	refl, err = s.Reflect(ctx, &memoryv1.ReflectRequest{OrgId: "o", BankId: "b", Query: "zebra"})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if refl.GetText() != "I don't know." {
		t.Fatalf("Reflect with no match = %q", refl.GetText())
	}
	if s.Retains() != 3 {
		t.Fatalf("Retains = %d, want 3", s.Retains())
	}
}

func TestListOperations(t *testing.T) {
	s := New(Options{FailedOps: 2})
	ctx := tenantCtx("t1")
	retain(t, s, ctx, "b", &memoryv1.RetainItem{Content: "a"})
	retain(t, s, ctx, "b", &memoryv1.RetainItem{Content: "b"})
	all, err := s.ListOperations(ctx, &memoryv1.ListOperationsRequest{OrgId: "o", BankId: "b"})
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}
	if len(all.GetOperations()) != 4 {
		t.Fatalf("got %d operations, want 4", len(all.GetOperations()))
	}
	failed, err := s.ListOperations(ctx, &memoryv1.ListOperationsRequest{
		OrgId: "o", BankId: "b", Status: memoryv1.OperationStatus_OPERATION_STATUS_FAILED,
	})
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}
	if len(failed.GetOperations()) != 2 {
		t.Fatalf("got %d failed operations, want 2", len(failed.GetOperations()))
	}
}
