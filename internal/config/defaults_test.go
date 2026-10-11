package config

import (
	"memd/internal/ai"
	"memd/internal/domain"
	"testing"
)

func TestServerDefaults(t *testing.T) {
	env := map[string]string{
		ai.EnvModel:                   "gpt-default",
		ai.EnvModelPrefix + "REFLECT": "gpt-reflect",
		ai.EnvModelPrefix + "RERANK":  "reranker",
	}
	cfg, err := ServerDefaults(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range llmSlots {
		want := "gpt-default"
		if s == domain.ModelSlotReflect {
			want = "gpt-reflect"
		}
		if got := modelOf(t, cfg, s); got != want {
			t.Errorf("slot %s = %q, want %q", s, got, want)
		}
	}
	if got := modelOf(t, cfg, domain.ModelSlotRerank); got != "reranker" {
		t.Errorf("rerank = %q", got)
	}
	if _, ok := cfg.Binding(domain.ModelSlotEmbed); ok {
		t.Error("EMBED bound in BankConfig")
	}
	checks := map[string]bool{
		"dedup 0.97":            *cfg.DedupThreshold == 0.97,
		"batch 8":               *cfg.ConsolidationLLMBatchSize == 8,
		"per-obs 256":           *cfg.ConsolidationSourceTokensPerObs == 256,
		"units per run 100":     *cfg.ConsolidationMaxUnitsPerRun == 100,
		"disposition 3":         *cfg.Disposition == domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3},
		"temporal link cap 20":  *cfg.Retain.TemporalLinkCap == 20,
		"entity match 0.6":      *cfg.Retain.EntityMatchThreshold == 0.6,
		"budget mid 300":        *cfg.Recall.BudgetMid == 300,
		"rrf 60":                *cfg.Recall.RRFK == 60,
		"rerank on when bound":  *cfg.Recall.Rerank,
		"budget clamp 20..2000": *cfg.Recall.BudgetMin == 20 && *cfg.Recall.BudgetMax == 2000,
		"max iterations 10":     *cfg.Reflect.MaxIterations == 10,
		"answer 4096":           *cfg.Reflect.MaxAnswerTokens == 4096,
		"debounce 30":           *cfg.MentalModels.RefreshDebounceSeconds == 30,
		"history 50":            *cfg.MentalModels.HistoryMaxEntries == 50,
		"agentic":               cfg.MentalModels.DefaultStrategy == domain.RefreshStrategyAgentic,
		"max combinations 15":   *cfg.DefaultObservationScopes.MaxCombinations == 15,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("default %s not set", name)
		}
	}
}

func TestServerDefaultsNoModels(t *testing.T) {
	cfg, err := ServerDefaults(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 0 {
		t.Fatalf("models = %+v, want none without env", cfg.Models)
	}
	if _, ok := cfg.Binding(domain.ModelSlotRerank); ok {
		t.Fatal("rerank bound without MEMD_MODEL_RERANK")
	}
	if cfg.Recall.Rerank == nil || *cfg.Recall.Rerank {
		t.Fatal("recall.rerank must default to false when RERANK is unbound")
	}
}
