package config

import (
	"errors"
	"memd/internal/domain"
	"reflect"
	"strings"
	"testing"
)

// expected restates SPEC "Per-request overrides" (with decision 401's readings) independently of the
// implementation's table: allowed path prefixes and slots per RPC.
var expected = map[string]struct {
	prefixes []string
	slots    []domain.ModelSlot
}{
	RPCRetain: {
		[]string{
			"retain_mission", "extraction_mode", "custom_extraction_prompt", "entity_labels",
			"entities_allow_free_form", "default_observation_scopes.", "retain.",
		},
		[]domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotTimeParse},
	},
	RPCReprocessDocument: {
		[]string{
			"retain_mission", "extraction_mode", "custom_extraction_prompt", "entity_labels",
			"entities_allow_free_form", "default_observation_scopes.", "retain.",
		},
		[]domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotTimeParse},
	},
	RPCRecall: {[]string{"recall."}, []domain.ModelSlot{domain.ModelSlotTimeParse, domain.ModelSlotRerank}},
	RPCReflect: {
		[]string{"reflect_mission", "disposition.", "reflect.", "recall."},
		[]domain.ModelSlot{domain.ModelSlotReflect, domain.ModelSlotStructuredOutput, domain.ModelSlotRerank},
	},
	RPCConsolidate: {
		[]string{
			"observations_mission", "max_observations_per_scope", "consolidation_source_facts_max_tokens",
			"consolidation_source_facts_max_tokens_per_observation", "consolidation_llm_batch_size",
			"consolidation_max_units_per_run", "dedup_threshold", "consolidation_strategies",
		},
		[]domain.ModelSlot{domain.ModelSlotConsolidate, domain.ModelSlotDedupCheck},
	},
	RPCUpdateMemory: {
		[]string{"retain.entity_match_threshold"},
		[]domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotTimeParse},
	},
	RPCRefreshMentalModel: {
		[]string{"reflect_mission", "disposition.", "reflect.", "mental_models."},
		[]domain.ModelSlot{
			domain.ModelSlotReflect, domain.ModelSlotSynthesize, domain.ModelSlotMentalModelDelta,
			domain.ModelSlotStructuredOutput,
		},
	},
	RPCMentalModelSpec: {
		[]string{"reflect_mission", "disposition.", "reflect.", "mental_models."},
		[]domain.ModelSlot{
			domain.ModelSlotReflect, domain.ModelSlotSynthesize, domain.ModelSlotMentalModelDelta,
			domain.ModelSlotStructuredOutput,
		},
	},
}

var allSlots = []domain.ModelSlot{
	domain.ModelSlotExtract, domain.ModelSlotTimeParse, domain.ModelSlotConsolidate, domain.ModelSlotDedupCheck,
	domain.ModelSlotReflect, domain.ModelSlotStructuredOutput, domain.ModelSlotMentalModelDelta,
	domain.ModelSlotRerank, domain.ModelSlotSynthesize,
}

// withPath returns a config that sets exactly path to some non-zero value.
func withPath(t *testing.T, path string) domain.BankConfig {
	t.Helper()
	idx, err := fieldIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg domain.BankConfig
	f := fieldAt(reflect.ValueOf(&cfg).Elem(), idx, true)
	f.Set(someValue(f.Type()))
	if got := setPaths(cfg); len(got) != 1 || got[0] != path {
		t.Fatalf("withPath(%q) sets %v", path, got)
	}
	return cfg
}

func someValue(t reflect.Type) reflect.Value {
	switch t.Kind() {
	case reflect.Pointer:
		v := reflect.New(t.Elem())
		v.Elem().Set(someValue(t.Elem()))
		return v
	case reflect.Slice:
		return reflect.MakeSlice(t, 1, 1)
	case reflect.Map:
		m := reflect.MakeMap(t)
		m.SetMapIndex(reflect.Zero(t.Key()), reflect.Zero(t.Elem()))
		return m
	case reflect.String:
		return reflect.ValueOf("x").Convert(t)
	case reflect.Bool:
		return reflect.ValueOf(true)
	case reflect.Int, reflect.Int64:
		return reflect.ValueOf(1).Convert(t)
	case reflect.Float64:
		return reflect.ValueOf(1.0)
	default:
		return reflect.Zero(t)
	}
}

func allowedPath(prefixes []string, p string) bool {
	for _, pre := range prefixes {
		if p == pre || strings.HasSuffix(pre, ".") && strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// TestValidateOverridesPathMatrix checks every leaf path against every RPC.
func TestValidateOverridesPathMatrix(t *testing.T) {
	for rpc, exp := range expected {
		for _, p := range leafPaths() {
			if p == "models" {
				continue
			}
			err := ValidateOverrides(rpc, withPath(t, p))
			if allowedPath(exp.prefixes, p) {
				if err != nil {
					t.Errorf("%s: %s rejected: %v", rpc, p, err)
				}
			} else if !errors.Is(err, domain.ErrInvalidArgument) {
				t.Errorf("%s: %s accepted, want INVALID_ARGUMENT", rpc, p)
			}
		}
		if err := ValidateOverrides(rpc, domain.BankConfig{}); err != nil {
			t.Errorf("%s: empty overrides rejected: %v", rpc, err)
		}
	}
}

// TestValidateOverridesSlotMatrix checks every slot against every RPC, including BankConfig.
func TestValidateOverridesSlotMatrix(t *testing.T) {
	for rpc, exp := range expected {
		for _, s := range allSlots {
			err := ValidateOverrides(rpc, domain.BankConfig{Models: []domain.SlotBinding{bind(s, "m")}})
			want := false
			for _, a := range exp.slots {
				want = want || a == s
			}
			if want && err != nil {
				t.Errorf("%s: slot %s rejected: %v", rpc, s, err)
			}
			if !want && !errors.Is(err, domain.ErrInvalidArgument) {
				t.Errorf("%s: slot %s accepted", rpc, s)
			}
		}
	}
	for _, s := range allSlots {
		cfg := domain.BankConfig{Models: []domain.SlotBinding{bind(s, "m")}}
		if err := ValidateOverrides(RPCBankConfig, cfg); err != nil {
			t.Errorf("BankConfig: slot %s rejected: %v", s, err)
		}
	}
}

// TestValidateOverridesBankConfigAllowsEveryPath: config-level writes accept every field.
func TestValidateOverridesBankConfigAllowsEveryPath(t *testing.T) {
	for _, p := range leafPaths() {
		if p == "models" {
			continue
		}
		if err := ValidateOverrides(RPCBankConfig, withPath(t, p)); err != nil {
			t.Errorf("%s rejected: %v", p, err)
		}
	}
}

// TestValidateOverridesBadSlots: rejected for every RPC.
func TestValidateOverridesBadSlots(t *testing.T) {
	bad := map[string]domain.BankConfig{
		"embed":       {Models: []domain.SlotBinding{bind(domain.ModelSlotEmbed, "m")}},
		"unspecified": {Models: []domain.SlotBinding{bind(domain.ModelSlotUnspecified, "m")}},
		"unknown":     {Models: []domain.SlotBinding{bind("bogus", "m")}},
		"no model":    {Models: []domain.SlotBinding{bind(domain.ModelSlotRerank, "")}},
		"duplicate": {Models: []domain.SlotBinding{
			bind(domain.ModelSlotRerank, "a"), bind(domain.ModelSlotRerank, "b"),
		}},
		"strategy slot": {ConsolidationStrategies: []domain.ConsolidationStrategy{{
			Name: "s", Models: []domain.SlotBinding{bind(domain.ModelSlotReflect, "m")},
		}}},
	}
	rpcs := []string{RPCBankConfig}
	for rpc := range expected {
		rpcs = append(rpcs, rpc)
	}
	for name, cfg := range bad {
		for _, rpc := range rpcs {
			if name == "strategy slot" && rpc != RPCConsolidate && rpc != RPCBankConfig {
				continue // rejected anyway: consolidation_strategies is not allowed there
			}
			if err := ValidateOverrides(rpc, cfg); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Errorf("%s on %s: err = %v", name, rpc, err)
			}
		}
	}
}

func TestValidateOverridesExamples(t *testing.T) {
	err := ValidateOverrides(RPCRecall, domain.BankConfig{RetainMission: "x"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("retain_mission on Recall: %v", err)
	}
	if err := ValidateOverrides("NoSuchRPC", domain.BankConfig{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("unknown rpc: %v", err)
	}
	ok := domain.BankConfig{
		ConsolidationStrategies: []domain.ConsolidationStrategy{{
			Name: "s", Models: []domain.SlotBinding{bind(domain.ModelSlotDedupCheck, "m")},
		}},
		Models: []domain.SlotBinding{bind(domain.ModelSlotConsolidate, "c")},
	}
	if err := ValidateOverrides(RPCConsolidate, ok); err != nil {
		t.Fatalf("valid consolidate overrides: %v", err)
	}
}

func TestValidateOverridesDisposition(t *testing.T) {
	for _, d := range []domain.Disposition{{Skepticism: 6}, {Literalism: -1}, {Empathy: 9}} {
		err := ValidateOverrides(RPCReflect, domain.BankConfig{Disposition: &d})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%+v accepted", d)
		}
	}
	ok := domain.Disposition{Skepticism: 1, Literalism: 5}
	if err := ValidateOverrides(RPCBankConfig, domain.BankConfig{Disposition: &ok}); err != nil {
		t.Fatalf("valid disposition rejected: %v", err)
	}
}

func TestPreviewRPC(t *testing.T) {
	cases := map[domain.PromptOperation]string{
		domain.PromptOperationRetain:           RPCRetain,
		domain.PromptOperationConsolidation:    RPCConsolidate,
		domain.PromptOperationReflect:          RPCReflect,
		domain.PromptOperationMentalModelDelta: RPCRefreshMentalModel,
		domain.PromptOperationSynthesize:       RPCRefreshMentalModel,
		domain.PromptOperationUnspecified:      "",
	}
	for op, want := range cases {
		if got := PreviewRPC(op); got != want {
			t.Errorf("PreviewRPC(%q) = %q, want %q", op, got, want)
		}
	}
}
