package bank

import (
	"errors"
	"memd/internal/domain"
	"slices"
	"testing"
)

// seedDirectives stores a global, a team-tagged, a sales-tagged and an inactive directive in b.
func seedDirectives(t *testing.T, e env, b domain.BankRef) {
	t.Helper()
	admin := NewDirectiveAdmin(e.store)
	for _, d := range []domain.Directive{
		{Bank: b, Name: "global", Rules: []string{"r"}, Active: true},
		{Bank: b, Name: "team", Tags: []string{" Team "}, Active: true},
		{Bank: b, Name: "sales", Tags: []string{"sales", "emea"}, Active: true},
		{Bank: b, Name: "off", Tags: []string{"team"}, Active: false},
	} {
		if _, err := admin.UpsertDirective(e.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectiveSelect(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	seedDirectives(t, e, b)
	sel := NewDirectiveSelector(e.store)
	cases := []struct {
		name string
		tags []string
		all  bool
		want []string
	}{
		{"no tags: global only", nil, false, []string{"global"}},
		{"matching tag", []string{"TEAM"}, false, []string{"global", "team"}},
		{"one of several", []string{"emea", "x"}, false, []string{"global", "sales"}},
		{"no overlap", []string{"x"}, false, []string{"global"}},
		{"all active", nil, true, []string{"global", "sales", "team"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sel.Select(e.ctx, b, tc.tags, tc.all)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(names(got), tc.want) {
				t.Errorf("Select(%v, %v) = %v, want %v", tc.tags, tc.all, names(got), tc.want)
			}
		})
	}
	if _, err := sel.Select(e.ctx, domain.BankRef{Tenant: "t", Bank: "nope"}, nil, false); !errors.Is(err,
		domain.ErrNotFound) {
		t.Errorf("unknown bank: %v", err)
	}
}

func TestDirectiveAdmin(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "new"}
	admin := NewDirectiveAdmin(e.store)
	if _, err := admin.ListDirectives(e.ctx, b, nil, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list on unknown bank: %v", err)
	}
	stored, err := admin.UpsertDirective(e.ctx, domain.Directive{Bank: b, Name: "a", Tags: []string{"B", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || !slices.Equal(stored.Tags, []string{"a", "b"}) {
		t.Fatalf("stored = %+v (upsert creates the bank and normalizes tags)", stored)
	}
	stored.Active, stored.Name = true, "renamed"
	replaced, err := admin.UpsertDirective(e.ctx, stored)
	if err != nil || replaced.ID != stored.ID || replaced.Name != "renamed" {
		t.Fatalf("replace = %+v %v", replaced, err)
	}
	seedDirectives(t, e, b)
	got, _ := admin.ListDirectives(e.ctx, b, []string{"team"}, false)
	if want := []string{"off", "team"}; !slices.Equal(names(got), want) {
		t.Errorf("list by tag = %v, want %v", names(got), want)
	}
	got, _ = admin.ListDirectives(e.ctx, b, nil, true)
	if want := []string{"global", "renamed", "sales", "team"}; !slices.Equal(names(got), want) {
		t.Errorf("list active = %v, want %v", names(got), want)
	}
	if err := admin.DeleteDirective(e.ctx, b, stored.ID); err != nil {
		t.Fatal(err)
	}
	if err := admin.DeleteDirective(e.ctx, b, stored.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestTenantAdmin(t *testing.T) {
	e := newEnv(t)
	tenants := NewTenantAdmin(e.store)
	cfg, err := tenants.TenantConfig(e.ctx, "t")
	if err != nil || cfg.RetainMission != "" {
		t.Fatalf("unset tenant config = %+v %v", cfg, err)
	}
	cfg, err = tenants.UpdateTenantConfig(e.ctx, "t", func(cur domain.BankConfig) (domain.BankConfig, error) {
		cur.RetainMission = "tenant mission"
		return cur, nil
	})
	if err != nil || cfg.RetainMission != "tenant mission" {
		t.Fatalf("patched tenant config = %+v %v", cfg, err)
	}
	boom := errors.New("boom")
	_, err = tenants.UpdateTenantConfig(e.ctx, "t", func(domain.BankConfig) (domain.BankConfig, error) {
		return domain.BankConfig{}, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("failing patch: %v", err)
	}
}

func TestBankAdmin(t *testing.T) {
	e := newEnv(t)
	banks := NewBankAdmin(e.store)
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	created, err := banks.CreateBank(e.ctx, domain.Bank{Ref: b, Name: "B", Config: domain.BankConfig{ReflectMission: "m"}})
	if err != nil || created.Name != "B" || created.Config.ReflectMission != "m" {
		t.Fatalf("created = %+v %v", created, err)
	}
	if _, err := banks.CreateBank(e.ctx, domain.Bank{Ref: b}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("second create: %v", err)
	}
	fresh := domain.BankRef{Tenant: "t", Bank: "fresh"}
	updated, err := banks.UpdateBankConfig(e.ctx, fresh, func(cur domain.BankConfig) (domain.BankConfig, error) {
		cur.ObservationsMission = "obs"
		return cur, nil
	})
	if err != nil || updated.Config.ObservationsMission != "obs" {
		t.Fatalf("update creates the bank: %+v %v", updated, err)
	}
	if _, err := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{}).Tombstone(e.ctx, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := banks.GetBank(e.ctx, fresh)
	if err != nil || !got.Deleting() {
		t.Errorf("GetBank of a tombstoned bank = %+v %v, want it with DeletingAt", got, err)
	}
	_, err = banks.UpdateBankConfig(e.ctx, fresh, func(c domain.BankConfig) (domain.BankConfig, error) { return c, nil })
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("update of a tombstoned bank: %v", err)
	}
}
