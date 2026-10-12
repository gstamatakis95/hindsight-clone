package main

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// wantStatus fails unless err has code and, when reason is set, that ErrorInfo reason.
func wantStatus(t *testing.T, what string, err error, code codes.Code, reason string) {
	t.Helper()
	st := status.Convert(err)
	got := ""
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			got = info.GetReason()
		}
	}
	if st.Code() != code || (reason != "" && got != reason) {
		t.Errorf("%s: %v (reason %q), want %v %s", what, err, got, code, reason)
	}
}

// TestOrgsMustExist: an org is never created on first use. A write or a bank config update under an unknown org is
// NOT_FOUND (ORG_NOT_FOUND) and creates nothing, while a write to an unknown bank of an existing org still creates
// the bank; CreateOrg twice is ALREADY_EXISTS.
func TestOrgsMustExist(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	mem, orgs := pb.NewMemoryServiceClient(w.conn), pb.NewOrgServiceClient(w.conn)
	_, err := mem.Retain(ctx, &pb.RetainRequest{OrgId: "typo", BankId: "b", Items: []*pb.RetainItem{{Content: "x"}}})
	wantStatus(t, "Retain under an unknown org", err, codes.NotFound, "ORG_NOT_FOUND")
	_, err = pb.NewConfigServiceClient(w.conn).UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
		OrgId: "typo", BankId: "b", Config: &pb.BankConfig{}, UpdateMask: &fieldmaskpb.FieldMask{},
	})
	wantStatus(t, "UpdateBankConfig under an unknown org", err, codes.NotFound, "ORG_NOT_FOUND")
	_, err = orgs.GetOrg(ctx, &pb.GetOrgRequest{OrgId: "typo"})
	wantStatus(t, "GetOrg after the rejected writes", err, codes.NotFound, "ORG_NOT_FOUND")
	_, err = orgs.CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: testOrg})
	wantStatus(t, "CreateOrg of an existing org", err, codes.AlreadyExists, "ALREADY_EXISTS")
	if _, err := mem.Retain(ctx, &pb.RetainRequest{
		OrgId: testOrg, BankId: "fresh", Items: []*pb.RetainItem{{Content: "Alice works as a nurse."}},
	}); err != nil {
		t.Fatalf("Retain to an unknown bank of an existing org: %v", err)
	}
	banks := pb.NewBankServiceClient(w.conn)
	if _, err := banks.GetBank(ctx, &pb.GetBankRequest{OrgId: testOrg, BankId: "fresh"}); err != nil {
		t.Errorf("GetBank of the bank the write created: %v", err)
	}
	_, err = orgs.DeleteOrg(ctx, &pb.DeleteOrgRequest{OrgId: testOrg})
	wantStatus(t, "DeleteOrg", err, codes.Unimplemented, "UNIMPLEMENTED")
	_, err = banks.ListBanks(ctx, &pb.ListBanksRequest{})
	wantStatus(t, "ListBanks without org_id", err, codes.InvalidArgument, "INVALID_ARGUMENT")
}

// TestListOrgsPaging: ListOrgs pages the tenant's orgs in id order with id_prefix, and another tenant's orgs never
// show.
func TestListOrgsPaging(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	orgs := pb.NewOrgServiceClient(w.conn)
	for _, id := range []string{"team-b", "team-a", "x"} {
		if _, err := orgs.CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := orgs.CreateOrg(w.ctxAs(t, "other"), &pb.CreateOrgRequest{OrgId: "team-c"}); err != nil {
		t.Fatal(err)
	}
	if got := listOrgs(ctx, t, orgs, "team-"); !slices.Equal(got, []string{"team-a", "team-b"}) {
		t.Errorf("ListOrgs(team-) = %v, want [team-a team-b]", got)
	}
	if got := listOrgs(ctx, t, orgs, ""); !slices.Equal(got, []string{testOrg, "team-a", "team-b", "x"}) {
		t.Errorf("ListOrgs = %v, want [%s team-a team-b x]", got, testOrg)
	}
}

// listOrgs pages through ListOrgs one org at a time and returns the ids.
func listOrgs(ctx context.Context, t *testing.T, orgs pb.OrgServiceClient, prefix string) []string {
	t.Helper()
	var ids []string
	req := &pb.ListOrgsRequest{IdPrefix: prefix, PageSize: 1}
	for range 10 {
		resp, err := orgs.ListOrgs(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range resp.GetOrgs() {
			ids = append(ids, o.GetId())
		}
		if resp.GetNextPageToken() == "" {
			return ids
		}
		req.PageToken = resp.GetNextPageToken()
	}
	t.Fatal("ListOrgs paging did not end")
	return nil
}

// TestOrgConfigLevel: the org config sits between the tenant's and the bank's. The org's effective config merges
// server < tenant < org, a bank's effective config includes the org level, and the bank's own overrides still win;
// CreateOrg validates its initial config as a whole level.
func TestOrgConfigLevel(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	cfg := pb.NewConfigServiceClient(w.conn)
	mission := func(m string) *pb.BankConfig { return &pb.BankConfig{Reflect: &pb.ReflectConfig{Mission: m}} }
	mask := &fieldmaskpb.FieldMask{Paths: []string{"reflect.mission"}}
	_, err := cfg.UpdateTenantConfig(ctx, &pb.UpdateTenantConfigRequest{Config: mission("tenant"), UpdateMask: mask})
	if err != nil {
		t.Fatal(err)
	}
	org, err := cfg.UpdateOrgConfig(ctx, &pb.UpdateOrgConfigRequest{
		OrgId: testOrg, Config: mission("org"), UpdateMask: mask,
	})
	if err != nil || org.GetConfig().GetEffective().GetReflect().GetMission() != "org" {
		t.Fatalf("UpdateOrgConfig = %v, %v; want effective mission org", org.GetConfig().GetEffective().GetReflect(), err)
	}
	bank, err := cfg.GetBankConfig(ctx, &pb.GetBankConfigRequest{OrgId: testOrg, BankId: w.bank(t, "cfg")})
	if err != nil || bank.GetConfig().GetEffective().GetReflect().GetMission() != "org" {
		t.Errorf("bank effective mission = %q, %v; want org", bank.GetConfig().GetEffective().GetReflect().GetMission(), err)
	}
	reset, err := cfg.ResetOrgConfig(ctx, &pb.ResetOrgConfigRequest{OrgId: testOrg, ResetMask: mask})
	if err != nil || reset.GetConfig().GetEffective().GetReflect().GetMission() != "tenant" {
		t.Errorf("ResetOrgConfig effective mission = %q, %v; want tenant",
			reset.GetConfig().GetEffective().GetReflect().GetMission(), err)
	}
	_, err = cfg.GetOrgConfig(ctx, &pb.GetOrgConfigRequest{OrgId: "typo"})
	wantStatus(t, "GetOrgConfig of an unknown org", err, codes.NotFound, "ORG_NOT_FOUND")
	_, err = pb.NewOrgServiceClient(w.conn).CreateOrg(ctx, &pb.CreateOrgRequest{
		OrgId: "bad", Config: &pb.BankConfig{Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Skepticism: 9}}},
	})
	wantStatus(t, "CreateOrg with an invalid config", err, codes.InvalidArgument, "")
}

// bank creates bank id in testOrg with a retain and returns its id.
func (w *wiredStack) bank(t *testing.T, id string) string {
	t.Helper()
	if _, err := pb.NewMemoryServiceClient(w.conn).Retain(w.ctx(t), &pb.RetainRequest{
		OrgId: testOrg, BankId: id, Items: []*pb.RetainItem{{Content: "Alice works as a nurse."}},
	}); err != nil {
		t.Fatal(err)
	}
	return id
}
