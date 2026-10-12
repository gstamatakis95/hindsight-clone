package configsvc

import (
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"slices"
	"sync"
	"testing"
	"time"
)

// TestTenantConfig: the view shows the tenant's own config and the effective one over the server defaults; a
// failing patch stores nothing; ResetTenantConfig unsets the paths (so the server default shows through again)
// and refuses no paths.
func TestTenantConfig(t *testing.T) {
	e := newEnv(t)
	server := domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "server"}}
	e.svc = New(Deps{DB: e.store, Config: config.NewResolver(e.store, server)})
	cfg, err := e.svc.GetTenantConfig(e.ctx, "t")
	if err != nil || cfg.Own.GetRetain().Mission != "" || cfg.Effective.GetReflect().Mission != "server" {
		t.Fatalf("unset tenant config = %+v %v; want an empty own config over the server defaults", cfg, err)
	}
	cfg, err = e.svc.UpdateTenantConfig(e.ctx, "t", func(cur domain.BankConfig) (domain.BankConfig, error) {
		retGrp := cur.GetRetain()
		retGrp.Mission = "tenant mission"
		cur.Retain = &retGrp
		return cur, nil
	})
	if err != nil || cfg.Own.GetRetain().Mission != "tenant mission" ||
		cfg.Effective.GetRetain().Mission != "tenant mission" {
		t.Fatalf("patched tenant config = %+v %v", cfg, err)
	}
	boom := errors.New("boom")
	_, err = e.svc.UpdateTenantConfig(e.ctx, "t", func(domain.BankConfig) (domain.BankConfig, error) {
		return domain.BankConfig{}, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("failing patch: %v", err)
	}
	if _, err := e.svc.ResetTenantConfig(e.ctx, "t", nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("reset with no paths: %v, want ErrInvalidArgument", err)
	}
	cfg, err = e.svc.ResetTenantConfig(e.ctx, "t", []string{"retain.mission"})
	if err != nil || cfg.Own.GetRetain().Mission != "" || cfg.Effective.GetReflect().Mission != "server" {
		t.Fatalf("reset tenant config = %+v %v", cfg, err)
	}
}

// TestBankConfig: an update creates an unknown bank and the view shows the own and effective config (the tenant
// level included); a reset unsets the paths and refuses no paths; a tombstoned bank reads as NOT_FOUND and refuses
// writes.
func TestBankConfig(t *testing.T) {
	e := newEnv(t)
	b := domain.NewBankRef("t", "org", "fresh")
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
	if err != nil || updated.Own.GetObservations().Mission != "obs" {
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
	if err != nil || reset.Own.GetObservations().Mission != "" {
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
		if _, err := e.svc.CreateDirective(e.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDirectives: CreateDirective creates the bank, assigns an id, normalizes tags and refuses a taken or empty
// name; UpdateDirective patches by id and refuses a taken name and an unknown directive; listing, deleting and a
// tombstoned bank.
func TestDirectives(t *testing.T) {
	e := newEnv(t)
	s := e.svc
	b := domain.NewBankRef("t", "org", "new")
	if _, err := s.ListDirectives(e.ctx, ListDirectivesRequest{Bank: b}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list on unknown bank: %v", err)
	}
	stored, err := s.CreateDirective(e.ctx, domain.Directive{ID: "ignored", Bank: b, Name: "a", Tags: []string{"B", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || stored.ID == "ignored" || !slices.Equal(stored.Tags, []string{"a", "b"}) {
		t.Fatalf("stored = %+v (create creates the bank, assigns an id and normalizes tags)", stored)
	}
	if _, err := s.CreateDirective(e.ctx, domain.Directive{Bank: b, Name: "a"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("create with a taken name: %v, want ErrAlreadyExists", err)
	}
	if _, err := s.CreateDirective(e.ctx, domain.Directive{Bank: b}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("create without a name: %v, want ErrInvalidArgument", err)
	}
	checkDirectiveUpdates(t, e, stored)
	checkDirectiveListing(t, e, b)
	if err := s.DeleteDirective(e.ctx, b, stored.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDirective(e.ctx, b, stored.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	e.tombstone(t, b)
	if _, err := s.CreateDirective(e.ctx, domain.Directive{Bank: b, Name: "x"}); !errors.Is(err,
		domain.ErrBankDeleting) {
		t.Errorf("CreateDirective on a deleting bank: %v, want ErrBankDeleting", err)
	}
	if _, err := s.ListDirectives(e.ctx, ListDirectivesRequest{Bank: b}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ListDirectives on a deleting bank: %v, want ErrNotFound", err)
	}
}

// checkDirectiveUpdates renames stored (and activates it), then refuses an unknown directive and, once the seed
// directives exist, a rename onto a taken name.
func checkDirectiveUpdates(t *testing.T, e env, stored domain.Directive) {
	t.Helper()
	s, b := e.svc, stored.Bank
	rename := func(name string) DirectivePatch {
		return func(cur domain.Directive) (domain.Directive, error) {
			cur.Name, cur.Active = name, true
			return cur, nil
		}
	}
	updated, err := s.UpdateDirective(e.ctx, b, stored.ID, rename("renamed"))
	if err != nil || updated.ID != stored.ID || updated.Name != "renamed" || !updated.Active ||
		!slices.Equal(updated.Tags, stored.Tags) {
		t.Fatalf("update = %+v %v", updated, err)
	}
	if _, err := s.UpdateDirective(e.ctx, b, "missing", rename("x")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update of an unknown directive: %v, want ErrNotFound", err)
	}
	seedDirectives(t, e, b)
	if _, err := s.UpdateDirective(e.ctx, b, stored.ID, rename("global")); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("rename onto a taken name: %v, want ErrAlreadyExists", err)
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

// overlapWait is how long the first config patch waits for the second to read the stored config too. With
// the read-modify-write serialized the second can't read until the first commits, so this wait times out.
const overlapWait = 500 * time.Millisecond

// racePatches runs two config patches the way two concurrent admins would: patch B starts once patch A
// has read the stored config, and A writes only after B has read it too (or after overlapWait, when B is
// blocked behind A). update applies one patch; it must run the patch inside its transaction.
func racePatches(
	t *testing.T, update func(Patch) error, setA, setB func(*domain.BankConfig),
) {
	t.Helper()
	aRead, bRead := make(chan struct{}), make(chan struct{})
	var aOnce, bOnce sync.Once
	patchA := func(cur domain.BankConfig) (domain.BankConfig, error) {
		aOnce.Do(func() { close(aRead) })
		select {
		case <-bRead:
		case <-time.After(overlapWait):
		}
		setA(&cur)
		return cur, nil
	}
	patchB := func(cur domain.BankConfig) (domain.BankConfig, error) {
		bOnce.Do(func() { close(bRead) })
		setB(&cur)
		return cur, nil
	}
	errA, errB := make(chan error, 1), make(chan error, 1)
	go func() { errA <- update(patchA) }()
	<-aRead
	go func() { errB <- update(patchB) }()
	if err := <-errA; err != nil {
		t.Fatalf("patch A: %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("patch B: %v", err)
	}
}

// TestConcurrentBankConfigPatchesKeepBoth: two concurrent UpdateBankConfig calls patching different fields
// both land. UpdateBankConfig takes the bank's seq (row lock) before reading the overrides.
func TestConcurrentBankConfigPatchesKeepBoth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := domain.NewBankRef("t-race", "org", "b-cfg")
	admin := e.svc
	if _, err := admin.UpdateBankConfig(t.Context(), b, func(c domain.BankConfig) (domain.BankConfig, error) {
		return c, nil
	}); err != nil {
		t.Fatalf("create bank: %v", err)
	}
	racePatches(t, func(p Patch) error {
		if _, err := admin.UpdateBankConfig(t.Context(), b, p); err != nil {
			return fmt.Errorf("update bank config: %w", err)
		}
		return nil
	}, setRetainMissionA, setReflectMissionB)
	got, err := admin.GetBankConfig(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Own.GetRetain().Mission != "A" || got.Own.GetReflect().Mission != "B" {
		t.Fatalf("lost update: stored config is retain.mission=%q reflect.mission=%q, want A and B",
			got.Own.GetRetain().Mission, got.Own.GetReflect().Mission)
	}
}

// TestConcurrentTenantConfigPatchesKeepBoth: the same for UpdateTenantConfig on a tenant with no stored
// config yet; it takes the tenant config lock before reading.
func TestConcurrentTenantConfigPatchesKeepBoth(t *testing.T) {
	t.Parallel()
	const tenant = "t-race"
	admin := newEnv(t).svc
	racePatches(t, func(p Patch) error {
		if _, err := admin.UpdateTenantConfig(t.Context(), tenant, p); err != nil {
			return fmt.Errorf("update tenant config: %w", err)
		}
		return nil
	}, setRetainMissionA, setReflectMissionB)
	got, err := admin.GetTenantConfig(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got.Own.GetRetain().Mission != "A" || got.Own.GetReflect().Mission != "B" {
		t.Fatalf("lost update: stored tenant config is retain.mission=%q reflect.mission=%q, want A and B",
			got.Own.GetRetain().Mission, got.Own.GetReflect().Mission)
	}
}

// setRetainMissionA is one racing patch: retain.mission = "A".
func setRetainMissionA(c *domain.BankConfig) {
	r := c.GetRetain()
	r.Mission = "A"
	c.Retain = &r
}

// setReflectMissionB is the other racing patch: reflect.mission = "B".
func setReflectMissionB(c *domain.BankConfig) {
	r := c.GetReflect()
	r.Mission = "B"
	c.Reflect = &r
}
