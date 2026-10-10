package config

import (
	"memd/internal/domain"
	"reflect"
	"testing"
)

func bind(slot domain.ModelSlot, model string) domain.SlotBinding {
	return domain.SlotBinding{Slot: slot, Binding: domain.ModelBinding{Model: model}}
}

func modelOf(t *testing.T, cfg domain.BankConfig, slot domain.ModelSlot) string {
	t.Helper()
	b, _ := cfg.Binding(slot)
	return b.Model
}

// TestMergePrecedence is the precedence table: server < tenant < bank < strategy < request (< item),
// one row per field shape.
func TestMergePrecedence(t *testing.T) {
	server := domain.BankConfig{
		RetainMission:  "server mission",
		ExtractionMode: domain.ExtractionModeConcise,
		DedupThreshold: ptr(0.97),
		EntityLabels:   []domain.EntityLabel{{Key: "server"}},
		Recall:         &domain.RecallConfig{RRFK: ptr(60), BudgetLow: ptr(100), EnabledStrategies: []string{"a", "b"}},
		Disposition:    &domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3},
		Models: []domain.SlotBinding{
			bind(domain.ModelSlotExtract, "s-ext"), bind(domain.ModelSlotRerank, "s-rr"),
			bind(domain.ModelSlotConsolidate, "s-con"),
		},
	}
	tenant := domain.BankConfig{
		RetainMission: "tenant mission",
		Recall:        &domain.RecallConfig{BudgetLow: ptr(50)},
		Models:        []domain.SlotBinding{bind(domain.ModelSlotExtract, "t-ext")},
	}
	bank := domain.BankConfig{
		DedupThreshold: ptr(0.9),
		EntityLabels:   []domain.EntityLabel{{Key: "bank1"}, {Key: "bank2"}},
		Disposition:    &domain.Disposition{Empathy: 5},
		Models:         []domain.SlotBinding{bind(domain.ModelSlotReflect, "b-ref"), bind(domain.ModelSlotRerank, "b-rr")},
	}
	strategy := StrategyLevel(domain.ConsolidationStrategy{
		ObservationsMission: "strategy obs",
		Models:              []domain.SlotBinding{bind(domain.ModelSlotConsolidate, "st-con")},
	})
	request := domain.BankConfig{
		Recall: &domain.RecallConfig{EnabledStrategies: []string{}},
		Models: []domain.SlotBinding{bind(domain.ModelSlotRerank, "r-rr")},
	}
	item := domain.BankConfig{RetainMission: "item mission"}

	got := Merge(server, tenant, bank, strategy, request, item)
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"most specific scalar wins", got.RetainMission, "item mission"},
		{"unset scalar inherits", got.ExtractionMode, domain.ExtractionModeConcise},
		{"optional scalar replaced", *got.DedupThreshold, 0.9},
		{"strategy level applies", got.ObservationsMission, "strategy obs"},
		{"list replaced, not appended", len(got.EntityLabels), 2},
		{"nested field from tenant", *got.Recall.BudgetLow, 50},
		{"nested field from server kept", *got.Recall.RRFK, 60},
		{"empty list clears", got.Recall.EnabledStrategies, []string{}},
		{"nested message merges by field", *got.Disposition, domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 5}},
		{"slot from tenant", modelOf(t, got, domain.ModelSlotExtract), "t-ext"},
		{"slot from bank", modelOf(t, got, domain.ModelSlotReflect), "b-ref"},
		{"slot from strategy", modelOf(t, got, domain.ModelSlotConsolidate), "st-con"},
		{"slot from request", modelOf(t, got, domain.ModelSlotRerank), "r-rr"},
		{"slot count", len(got.Models), 4},
	}
	for _, tc := range tests {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

// TestMergeRequestRerankOnlyKeepsBankSlots: a request setting only RERANK keeps the bank's other slots.
func TestMergeRequestRerankOnlyKeepsBankSlots(t *testing.T) {
	bank := domain.BankConfig{Models: []domain.SlotBinding{
		bind(domain.ModelSlotExtract, "b-ext"), bind(domain.ModelSlotReflect, "b-ref"),
		bind(domain.ModelSlotRerank, "b-rr"), bind(domain.ModelSlotTimeParse, "b-tp"),
	}}
	request := domain.BankConfig{Models: []domain.SlotBinding{bind(domain.ModelSlotRerank, "r-rr")}}
	got := Merge(domain.BankConfig{}, domain.BankConfig{}, bank, request)
	want := map[domain.ModelSlot]string{
		domain.ModelSlotExtract: "b-ext", domain.ModelSlotReflect: "b-ref",
		domain.ModelSlotRerank: "r-rr", domain.ModelSlotTimeParse: "b-tp",
	}
	if len(got.Models) != len(want) {
		t.Fatalf("got %d slots, want %d", len(got.Models), len(want))
	}
	for slot, model := range want {
		if m := modelOf(t, got, slot); m != model {
			t.Errorf("slot %s = %q, want %q", slot, m, model)
		}
	}
}

// TestMergeBindingReplacedWhole: the most specific level supplies the whole binding (ruling R4).
func TestMergeBindingReplacedWhole(t *testing.T) {
	bank := domain.BankConfig{Models: []domain.SlotBinding{{
		Slot: domain.ModelSlotReflect, Binding: domain.ModelBinding{Model: "b", Temperature: ptr(0.7)},
	}}}
	req := domain.BankConfig{Models: []domain.SlotBinding{bind(domain.ModelSlotReflect, "r")}}
	b, _ := Merge(bank, req).Binding(domain.ModelSlotReflect)
	if b.Model != "r" || b.Temperature != nil {
		t.Fatalf("binding = %+v, want model r with no inherited temperature", b)
	}
}

// TestMergeDoesNotAliasInputs: mutating the result never reaches the inputs, and inputs are unchanged.
func TestMergeDoesNotAliasInputs(t *testing.T) {
	server := domain.BankConfig{
		Recall:       &domain.RecallConfig{RRFK: ptr(60), StrategyBoosts: map[string]float64{"semantic": 1}},
		EntityLabels: []domain.EntityLabel{{Key: "k", Optional: ptr(true)}},
		Models: []domain.SlotBinding{{
			Slot: domain.ModelSlotExtract, Binding: domain.ModelBinding{Model: "m", Extra: map[string]any{"a": 1}},
		}},
	}
	bank := domain.BankConfig{Recall: &domain.RecallConfig{BudgetLow: ptr(5)}}
	snapServer, snapBank := Merge(server), Merge(bank)

	got := Merge(server, bank)
	*got.Recall.RRFK = 1
	*got.Recall.BudgetLow = 1
	got.Recall.StrategyBoosts["semantic"] = 9
	*got.EntityLabels[0].Optional = false
	got.Models[0].Binding.Extra["a"] = 2
	got.Models[0].Binding.Model = "x"

	if !reflect.DeepEqual(server, snapServer) || !reflect.DeepEqual(bank, snapBank) {
		t.Fatal("Merge result aliases an input")
	}
}

// TestMergeEmpty: no levels and all-unset levels give the zero config.
func TestMergeEmpty(t *testing.T) {
	if got := Merge(); !reflect.DeepEqual(got, domain.BankConfig{}) {
		t.Fatalf("Merge() = %+v", got)
	}
	if got := Merge(domain.BankConfig{}, domain.BankConfig{}); !reflect.DeepEqual(got, domain.BankConfig{}) {
		t.Fatalf("Merge(zero, zero) = %+v", got)
	}
}
