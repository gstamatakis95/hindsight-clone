package config

import (
	"encoding/json"
	"memd/internal/ai"
	"memd/internal/domain"
	"reflect"
	"testing"
)

// bindings returns a ModelBindings with default (when non-empty) and the given slot bindings, each to a model of
// that name.
func bindings(def string, slots map[domain.ModelSlot]string) *domain.ModelBindings {
	m := &domain.ModelBindings{}
	if def != "" {
		m.Default = &domain.ModelBinding{Model: def}
	}
	for s, model := range slots {
		*m.Slot(s) = &domain.ModelBinding{Model: model}
	}
	return m
}

// modelOf is the model ai.Router resolves for slot on cfg, or "" when the slot is unbound.
func modelOf(t *testing.T, cfg domain.BankConfig, slot domain.ModelSlot) string {
	t.Helper()
	b, err := ai.NewRouter().Resolve(cfg, slot)
	if err != nil {
		return ""
	}
	return b.Model
}

// TestMergePrecedence is the precedence table: server < tenant < bank < strategy < request (< item), one row
// per field shape.
func TestMergePrecedence(t *testing.T) {
	server := domain.BankConfig{
		Retain: &domain.RetainConfig{
			Mission: "server mission", ExtractionMode: domain.ExtractionModeConcise,
			EntityLabels: []domain.EntityLabel{{Key: "server"}},
		},
		Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.97)},
		Recall:       &domain.RecallConfig{RRFK: ptr(60), BudgetLow: ptr(100), EnabledStrategies: []string{"a", "b"}},
		Reflect:      &domain.ReflectConfig{Disposition: &domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3}},
	}
	tenant := domain.BankConfig{
		Retain: &domain.RetainConfig{Mission: "tenant mission"},
		Recall: &domain.RecallConfig{BudgetLow: ptr(50)},
	}
	bank := domain.BankConfig{
		Retain:       &domain.RetainConfig{EntityLabels: []domain.EntityLabel{{Key: "bank1"}, {Key: "bank2"}}},
		Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.9)},
		Reflect:      &domain.ReflectConfig{Disposition: &domain.Disposition{Empathy: 5}},
	}
	strategy := StrategyLevel(domain.ConsolidationStrategy{Mission: "strategy obs"})
	request := domain.BankConfig{Recall: &domain.RecallConfig{EnabledStrategies: []string{}}}
	item := domain.BankConfig{Retain: &domain.RetainConfig{Mission: "item mission"}}

	got := Merge(server, tenant, bank, strategy, request, item)
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"most specific scalar wins", got.Retain.Mission, "item mission"},
		{"unset scalar inherits", got.Retain.ExtractionMode, domain.ExtractionModeConcise},
		{"optional scalar replaced", *got.Observations.DedupThreshold, 0.9},
		{"strategy level applies", got.Observations.Mission, "strategy obs"},
		{"list replaced, not appended", len(got.Retain.EntityLabels), 2},
		{"nested field from tenant", *got.Recall.BudgetLow, 50},
		{"nested field from server kept", *got.Recall.RRFK, 60},
		{"empty list clears", got.Recall.EnabledStrategies, []string{}},
		{
			"nested message merges by field", *got.Reflect.Disposition,
			domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 5},
		},
	}
	for _, tc := range tests {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

// TestModelResolution pins the model-routing rule: for each slot, the most specific level that yields a binding
// wins, where a level yields its own binding for the slot, else its default (chat-model slots only). A binding is
// used whole, and rerank and embed are never filled by a default.
func TestModelResolution(t *testing.T) {
	type level = *domain.ModelBindings
	ext, ref, rr := domain.ModelSlotExtract, domain.ModelSlotReflect, domain.ModelSlotRerank
	tests := []struct {
		name   string
		levels []level // least to most specific
		slot   domain.ModelSlot
		want   string
	}{
		{"default binds every chat slot", []level{bindings("d", nil)}, ref, "d"},
		{
			"a slot beats its own level's default",
			[]level{bindings("d", map[domain.ModelSlot]string{ext: "e"})},
			ext, "e",
		},
		{"a bank default beats a server slot", []level{
			bindings("sd", map[domain.ModelSlot]string{ext: "se"}),
			bindings("bd", nil),
		}, ext, "bd"},
		{"a request slot beats a bank default", []level{
			bindings("bd", nil),
			bindings("", map[domain.ModelSlot]string{ext: "re"}),
		}, ext, "re"},
		{"a request slot leaves the other slots", []level{
			bindings("bd", nil),
			bindings("", map[domain.ModelSlot]string{ext: "re"}),
		}, ref, "bd"},
		{"a level with neither inherits", []level{
			bindings("sd", map[domain.ModelSlot]string{ref: "sr"}), nil,
			bindings("", map[domain.ModelSlot]string{ext: "x"}),
		}, ref, "sr"},
		{"rerank is not filled by a default", []level{bindings("d", nil)}, rr, ""},
		{"a bank default keeps the server's rerank", []level{
			bindings("", map[domain.ModelSlot]string{rr: "r"}),
			bindings("bd", nil),
		}, rr, "r"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var levels []domain.BankConfig
			for _, m := range tc.levels {
				levels = append(levels, domain.BankConfig{Models: m})
			}
			if got := modelOf(t, Merge(levels...), tc.slot); got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.slot, got, tc.want)
			}
		})
	}
	t.Run("the winning binding is used whole", func(t *testing.T) {
		bank := domain.BankConfig{Models: &domain.ModelBindings{
			Reflect: &domain.ModelBinding{Model: "b", Temperature: ptr(0.7)},
		}}
		req := domain.BankConfig{Models: bindings("", map[domain.ModelSlot]string{ref: "r"})}
		b, _ := Merge(bank, req).Binding(ref)
		if b.Model != "r" || b.Temperature != nil {
			t.Fatalf("binding = %+v, want model r with no inherited temperature", b)
		}
	})
}

// TestMergeDoesNotAliasInputs: mutating the result never reaches the inputs, and inputs are unchanged.
func TestMergeDoesNotAliasInputs(t *testing.T) {
	server := domain.BankConfig{
		Recall: &domain.RecallConfig{RRFK: ptr(60), StrategyBoosts: map[string]float64{"semantic": 1}},
		Retain: &domain.RetainConfig{EntityLabels: []domain.EntityLabel{{Key: "k", Optional: ptr(true)}}},
		Models: &domain.ModelBindings{Default: &domain.ModelBinding{Model: "m", Extra: map[string]any{"a": 1}}},
	}
	bank := domain.BankConfig{Recall: &domain.RecallConfig{BudgetLow: ptr(5)}}
	snap := func() string {
		b, err := json.Marshal([]domain.BankConfig{server, bank})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := snap()

	got := Merge(server, bank)
	*got.Recall.RRFK = 1
	*got.Recall.BudgetLow = 1
	got.Recall.StrategyBoosts["semantic"] = 9
	*got.Retain.EntityLabels[0].Optional = false
	got.Models.Extract.Extra["a"] = 2
	got.Models.Default.Model = "x"

	if snap() != before {
		t.Fatal("Merge result aliases an input")
	}
}
