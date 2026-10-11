package config

import (
	"errors"
	"memd/internal/domain"
	"testing"
)

func TestServerDefaults(t *testing.T) {
	file := domain.BankConfig{
		Models:       bindings("gpt-default", map[domain.ModelSlot]string{domain.ModelSlotReflect: "gpt-reflect"}),
		Observations: &domain.ObservationConfig{ReconcileGraceSeconds: ptr(60)},
	}
	file.Models.Rerank = &domain.ModelBinding{Model: "reranker"}
	cfg, err := ServerDefaults(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range domain.LLMSlots {
		want := "gpt-default"
		if s == domain.ModelSlotReflect {
			want = "gpt-reflect"
		}
		if got := modelOf(t, cfg, s); got != want {
			t.Errorf("slot %s = %q, want %q", s, got, want)
		}
	}
	checks := map[string]bool{
		"rerank bound":          modelOf(t, cfg, domain.ModelSlotRerank) == "reranker",
		"embed default":         cfg.Models.Embed != nil && cfg.Models.Embed.Model == DefaultEmbedModel,
		"file wins":             *cfg.Observations.ReconcileGraceSeconds == 60,
		"dedup 0.97":            *cfg.Observations.DedupThreshold == 0.97,
		"batch 8":               *cfg.Observations.LLMBatchSize == 8,
		"per-obs 256":           *cfg.Observations.SourceFactsMaxTokensPerObservation == 256,
		"units per run 100":     *cfg.Observations.MaxUnitsPerRun == 100,
		"disposition 3":         *cfg.Reflect.Disposition == domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3},
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
		"max combinations 15":   *cfg.Retain.DefaultObservationScopeModes.MaxCombinations == 15,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("default %s not set", name)
		}
	}
}

// TestServerDefaultsRerank: without a rerank binding recall.rerank defaults to false (recall skips the
// cross-encoder instead of failing); asking for rerank without a binding is an error.
func TestServerDefaultsRerank(t *testing.T) {
	cfg, err := ServerDefaults(domain.BankConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if modelOf(t, cfg, domain.ModelSlotExtract) != "" || modelOf(t, cfg, domain.ModelSlotRerank) != "" {
		t.Fatal("models bound without a defaults.models block")
	}
	if cfg.Recall.Rerank == nil || *cfg.Recall.Rerank {
		t.Fatal("recall.rerank must default to false when RERANK is unbound")
	}
	_, err = ServerDefaults(domain.BankConfig{Recall: &domain.RecallConfig{Rerank: ptr(true)}})
	if err == nil {
		t.Fatal("recall.rerank true without models.rerank accepted")
	}
	_, err = ServerDefaults(domain.BankConfig{Reflect: &domain.ReflectConfig{
		Disposition: &domain.Disposition{Empathy: 9},
	}})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("bad disposition: err = %v, want InvalidArgument", err)
	}
}
