package config

import (
	"errors"
	"memd/internal/domain"
	"reflect"
	"testing"
)

func TestApplyMask(t *testing.T) {
	base := domain.BankConfig{
		RetainMission:  "keep",
		ReflectMission: "old reflect",
		EntityLabels:   []domain.EntityLabel{{Key: "a"}},
		Recall:         &domain.RecallConfig{RRFK: ptr(10), BudgetLow: ptr(5)},
		Models:         []domain.SlotBinding{bind(domain.ModelSlotExtract, "e")},
	}
	patch := domain.BankConfig{
		ReflectMission: "new reflect",
		RetainMission:  "patched retain",
		Recall:         &domain.RecallConfig{RRFK: ptr(99)},
	}
	with := func(edit func(*domain.BankConfig)) domain.BankConfig {
		c := Merge(base)
		edit(&c)
		return c
	}
	tests := []struct {
		name  string
		paths []string
		want  domain.BankConfig
	}{
		{
			"masked scalar set; unmasked ignored",
			[]string{"reflect_mission"},
			with(func(c *domain.BankConfig) { c.ReflectMission = "new reflect" }),
		},
		{
			"masked list cleared to explicit empty",
			[]string{"entity_labels"},
			with(func(c *domain.BankConfig) { c.EntityLabels = []domain.EntityLabel{} }),
		},
		{
			"masked nested leaf",
			[]string{"recall.rrf_k"},
			with(func(c *domain.BankConfig) { c.Recall.RRFK = ptr(99) }),
		},
		{
			"masked nested leaf unset in patch unsets it",
			[]string{"recall.budget_low"},
			with(func(c *domain.BankConfig) { c.Recall.BudgetLow = nil }),
		},
		{
			"masked message replaced whole",
			[]string{"recall"},
			with(func(c *domain.BankConfig) { c.Recall = &domain.RecallConfig{RRFK: ptr(99)} }),
		},
		{
			"masked models replaced",
			[]string{"models"},
			with(func(c *domain.BankConfig) { c.Models = []domain.SlotBinding{} }),
		},
		{"masked absent message unsets", []string{"reflect"}, base},
		{"empty mask merges set fields", nil, with(func(c *domain.BankConfig) {
			c.ReflectMission, c.RetainMission, c.Recall.RRFK = "new reflect", "patched retain", ptr(99)
		})},
	}
	snap := Merge(base)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyMask(base, patch, tc.paths)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
			if !reflect.DeepEqual(base, snap) {
				t.Fatal("ApplyMask modified base")
			}
		})
	}
}

// TestMaskedClearOverridesLowerLevel: a list cleared through a mask clears the tenant's list on merge.
func TestMaskedClearOverridesLowerLevel(t *testing.T) {
	tenant := domain.BankConfig{EntityLabels: []domain.EntityLabel{{Key: "tenant"}}}
	bank, err := ApplyMask(domain.BankConfig{}, domain.BankConfig{}, []string{"entity_labels"})
	if err != nil {
		t.Fatal(err)
	}
	got := Merge(tenant, bank).EntityLabels
	if got == nil || len(got) != 0 {
		t.Fatalf("entity_labels = %#v, want cleared", got)
	}
}

func TestApplyMaskPrunesEmptyMessages(t *testing.T) {
	base := domain.BankConfig{Recall: &domain.RecallConfig{RRFK: ptr(10)}}
	got, err := ApplyMask(base, domain.BankConfig{}, []string{"recall.rrf_k"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Recall != nil {
		t.Fatalf("recall = %+v, want nil", *got.Recall)
	}
}

func TestMaskUnknownPath(t *testing.T) {
	for _, p := range []string{"nope", "recall.nope", "retain_mission.x", "", "recall."} {
		_, err := ApplyMask(domain.BankConfig{}, domain.BankConfig{}, []string{p})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("ApplyMask(%q) err = %v", p, err)
		}
		_, err = ResetPaths(domain.BankConfig{RetainMission: "x"}, []string{p})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("ResetPaths(%q) err = %v", p, err)
		}
	}
}

func TestResetPaths(t *testing.T) {
	cfg := domain.BankConfig{
		RetainMission: "m",
		EntityLabels:  []domain.EntityLabel{},
		Recall:        &domain.RecallConfig{RRFK: ptr(1), BudgetLow: ptr(2)},
	}
	got, err := ResetPaths(cfg, []string{"entity_labels", "recall.rrf_k"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.BankConfig{RetainMission: "m", Recall: &domain.RecallConfig{BudgetLow: ptr(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	all, err := ResetPaths(cfg, nil)
	if err != nil || !reflect.DeepEqual(all, domain.BankConfig{}) {
		t.Fatalf("reset all = %+v, %v", all, err)
	}
}

func TestSetPathsAndLeafPaths(t *testing.T) {
	cfg := domain.BankConfig{
		RetainMission: "m",
		EntityLabels:  []domain.EntityLabel{},
		Recall:        &domain.RecallConfig{RRFK: ptr(1)},
		Reflect:       &domain.ReflectConfig{},
		Models:        []domain.SlotBinding{bind(domain.ModelSlotExtract, "e")},
	}
	want := []string{"entity_labels", "models", "recall.rrf_k", "retain_mission"}
	if got := SetPaths(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("SetPaths = %v, want %v", got, want)
	}
	leaves := map[string]bool{}
	for _, l := range LeafPaths() {
		leaves[l] = true
	}
	for _, p := range append(want, "disposition.empathy", "mental_models.default_strategy",
		"default_observation_scopes.max_combinations", "consolidation_source_facts_max_tokens_per_observation") {
		if _, err := fieldIndex(p); err != nil {
			t.Errorf("fieldIndex(%q): %v", p, err)
		}
		if !leaves[p] {
			t.Errorf("LeafPaths lacks %q", p)
		}
	}
}
