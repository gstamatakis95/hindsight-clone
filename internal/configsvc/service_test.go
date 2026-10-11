package configsvc

import (
	"errors"
	"memd/internal/domain"
	"slices"
	"testing"
)

func TestTenantConfig(t *testing.T) {
	e := newEnv(t)
	cfg, err := e.svc.GetTenantConfig(e.ctx, "t")
	if err != nil || cfg.GetRetain().Mission != "" {
		t.Fatalf("unset tenant config = %+v %v", cfg, err)
	}
	cfg, err = e.svc.UpdateTenantConfig(e.ctx, "t", func(cur domain.BankConfig) (domain.BankConfig, error) {
		retGrp := cur.GetRetain()
		retGrp.Mission = "tenant mission"
		cur.Retain = &retGrp
		return cur, nil
	})
	if err != nil || cfg.GetRetain().Mission != "tenant mission" {
		t.Fatalf("patched tenant config = %+v %v", cfg, err)
	}
	boom := errors.New("boom")
	_, err = e.svc.UpdateTenantConfig(e.ctx, "t", func(domain.BankConfig) (domain.BankConfig, error) {
		return domain.BankConfig{}, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("failing patch: %v", err)
	}
}

// TestBankConfig: an update creates an unknown bank and the view shows the own and effective config (the tenant
// level included); a reset unsets the paths and refuses no paths; a tombstoned bank reads as NOT_FOUND and refuses
// writes.
func TestBankConfig(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "fresh"}
	if _, err := e.svc.UpdateTenantConfig(e.ctx, "t", func(cur domain.BankConfig) (domain.BankConfig, error) {
		cur.Reflect = &domain.ReflectConfig{Mission: "tenant"}
		return cur, nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := e.svc.UpdateBankConfig(e.ctx, b, func(cur domain.BankConfig) (domain.BankConfig, error) {
		cur.Observations = &domain.ObservationConfig{Mission: "obs"}
		return cur, nil
	})
	if err != nil || updated.Config.GetObservations().Mission != "obs" {
		t.Fatalf("update creates the bank: %+v %v", updated, err)
	}
	view, err := e.svc.GetBankConfig(e.ctx, b)
	if err != nil || view.Own.GetObservations().Mission != "obs" || view.Own.Reflect != nil ||
		view.Effective.GetReflect().Mission != "tenant" || view.Effective.GetObservations().Mission != "obs" {
		t.Fatalf("view = %+v %v", view, err)
	}
	if _, err := e.svc.ResetBankConfig(e.ctx, b, nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("reset with no paths: %v, want ErrInvalidArgument", err)
	}
	reset, err := e.svc.ResetBankConfig(e.ctx, b, []string{"observations.mission"})
	if err != nil || reset.Config.GetObservations().Mission != "" {
		t.Fatalf("reset = %+v %v", reset, err)
	}
	e.tombstone(t, b)
	if _, err := e.svc.GetBankConfig(e.ctx, b); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetBankConfig of a tombstoned bank: %v, want ErrNotFound", err)
	}
	_, err = e.svc.UpdateBankConfig(e.ctx, b, func(c domain.BankConfig) (domain.BankConfig, error) { return c, nil })
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("update of a tombstoned bank: %v", err)
	}
}

// seedDirectives stores a global, a team-tagged, a sales-tagged and an inactive directive in b.
func seedDirectives(t *testing.T, e env, b domain.BankRef) {
	t.Helper()
	for _, d := range []domain.Directive{
		{Bank: b, Name: "global", Rules: []string{"r"}, Active: true},
		{Bank: b, Name: "team", Tags: []string{" Team "}, Active: true},
		{Bank: b, Name: "sales", Tags: []string{"sales", "emea"}, Active: true},
		{Bank: b, Name: "off", Tags: []string{"team"}, Active: false},
	} {
		if _, err := e.svc.UpsertDirective(e.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectives(t *testing.T) {
	e := newEnv(t)
	s := e.svc
	b := domain.BankRef{Tenant: "t", Bank: "new"}
	if _, err := s.ListDirectives(e.ctx, ListDirectivesRequest{Bank: b}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list on unknown bank: %v", err)
	}
	stored, err := s.UpsertDirective(e.ctx, domain.Directive{Bank: b, Name: "a", Tags: []string{"B", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || !slices.Equal(stored.Tags, []string{"a", "b"}) {
		t.Fatalf("stored = %+v (upsert creates the bank and normalizes tags)", stored)
	}
	stored.Active, stored.Name = true, "renamed"
	replaced, err := s.UpsertDirective(e.ctx, stored)
	if err != nil || replaced.ID != stored.ID || replaced.Name != "renamed" {
		t.Fatalf("replace = %+v %v", replaced, err)
	}
	seedDirectives(t, e, b)
	checkDirectiveListing(t, e, b)
	if err := s.DeleteDirective(e.ctx, b, stored.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDirective(e.ctx, b, stored.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	e.tombstone(t, b)
	if _, err := s.UpsertDirective(e.ctx, domain.Directive{Bank: b, Name: "x"}); !errors.Is(err,
		domain.ErrBankDeleting) {
		t.Errorf("UpsertDirective on a deleting bank: %v, want ErrBankDeleting", err)
	}
	if _, err := s.ListDirectives(e.ctx, ListDirectivesRequest{Bank: b}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ListDirectives on a deleting bank: %v, want ErrNotFound", err)
	}
}

// checkDirectiveListing checks ListDirectives' filters over the seeded directives, paging one at a time: a tag
// (any_strict by default), active only, an explicit any (untagged ones pass), and tag groups refused.
func checkDirectiveListing(t *testing.T, e env, b domain.BankRef) {
	t.Helper()
	for _, tc := range []struct {
		req  ListDirectivesRequest
		want []string
	}{
		{ListDirectivesRequest{Bank: b, Tags: domain.TagFilter{Tags: []string{"team"}}}, []string{"off", "team"}},
		{ListDirectivesRequest{Bank: b, ActiveOnly: true}, []string{"global", "renamed", "sales", "team"}},
		{
			ListDirectivesRequest{Bank: b, Tags: domain.TagFilter{Tags: []string{"team", "sales"}, Match: domain.TagsMatchAny}},
			[]string{"global", "off", "sales", "team"},
		},
	} {
		if got := names(listAllDirectives(t, e, tc.req)); !slices.Equal(got, tc.want) {
			t.Errorf("list %+v = %v, want %v", tc.req, got, tc.want)
		}
	}
	groups := ListDirectivesRequest{Bank: b, Tags: domain.TagFilter{Groups: []domain.TagGroup{{
		Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"team"}},
	}}}}
	if _, err := e.svc.ListDirectives(e.ctx, groups); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("list with tag groups: %v, want ErrInvalidArgument", err)
	}
}

// listAllDirectives pages through req one directive at a time and checks the keyset order (created_at, id).
func listAllDirectives(t *testing.T, e env, req ListDirectivesRequest) []domain.Directive {
	t.Helper()
	req.PageSize = 1
	var out []domain.Directive
	for range 20 {
		page, err := e.svc.ListDirectives(e.ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page.Items...)
		if page.NextPageToken == "" {
			for i := 1; i < len(out); i++ {
				if out[i].CreatedAt.Before(out[i-1].CreatedAt) {
					t.Fatalf("directive pages out of order: %v", names(out))
				}
			}
			return out
		}
		req.PageToken = page.NextPageToken
	}
	t.Fatal("directive paging did not end")
	return nil
}
