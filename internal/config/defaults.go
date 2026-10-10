package config

import (
	"fmt"
	"memd/internal/domain"
	"strings"
)

// Environment variables read by ServerDefaults.
const (
	// EnvModel is the default gateway model for every LLM slot (all but RERANK and EMBED).
	EnvModel = "MEMD_MODEL"
	// EnvModelPrefix + an upper-case slot name (MEMD_MODEL_EXTRACT, MEMD_MODEL_RERANK, ...) binds one slot,
	// winning over EnvModel. RERANK is bound only this way.
	EnvModelPrefix = "MEMD_MODEL_"
)

// llmSlots are the slots EnvModel binds; bindableSlots adds RERANK. EMBED is server-wide and never
// part of a BankConfig.
var (
	llmSlots = []domain.ModelSlot{
		domain.ModelSlotExtract, domain.ModelSlotTimeParse, domain.ModelSlotConsolidate,
		domain.ModelSlotDedupCheck, domain.ModelSlotReflect, domain.ModelSlotStructuredOutput,
		domain.ModelSlotMentalModelDelta, domain.ModelSlotSynthesize,
	}
	bindableSlots = append(append([]domain.ModelSlot{}, llmSlots...), domain.ModelSlotRerank)
)

// ServerDefaults is the server level of the config hierarchy: every default the spec states, plus the
// model bindings from the environment (EnvModel for every LLM slot, EnvModelPrefix+SLOT per slot).
// Knobs whose default the spec leaves open stay unset; the engine that reads them owns that default.
// A slot with no model in the environment stays unbound, and port.ModelRouter rejects calls on it.
func ServerDefaults(env func(string) string) (domain.BankConfig, error) {
	cfg := specDefaults()
	def := strings.TrimSpace(env(EnvModel))
	for _, s := range bindableSlots {
		model := strings.TrimSpace(env(EnvModelPrefix + strings.ToUpper(string(s))))
		if model == "" && s != domain.ModelSlotRerank {
			model = def
		}
		if model != "" {
			cfg.Models = append(cfg.Models, domain.SlotBinding{Slot: s, Binding: domain.ModelBinding{Model: model}})
		}
	}
	if err := ValidateOverrides(RPCBankConfig, cfg); err != nil {
		return domain.BankConfig{}, fmt.Errorf("config: server defaults: %w", err)
	}
	return cfg, nil
}

// specDefaults holds the defaults stated in the spec's BankConfig comments.
func specDefaults() domain.BankConfig {
	return domain.BankConfig{
		Disposition:                     &domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3},
		ExtractionMode:                  domain.ExtractionModeConcise,
		AutoConsolidation:               ptr(true),
		DedupThreshold:                  ptr(0.97),
		Recall:                          recallDefaults(),
		Retain:                          &domain.RetainTuning{TemporalLinkCap: ptr(20)},
		Reflect:                         reflectDefaults(),
		MentalModels:                    mentalModelDefaults(),
		DefaultObservationScopes:        &domain.ObservationScopes{Mode: domain.ScopeModeCombined, MaxCombinations: ptr(15)},
		EnableObservations:              ptr(true),
		ConsolidationLLMBatchSize:       ptr(8),
		ConsolidationSourceTokensPerObs: ptr(256),
		EntitiesAllowFreeForm:           ptr(true),
		ConsolidationMaxUnitsPerRun:     ptr(100),
	}
}

func recallDefaults() *domain.RecallConfig {
	return &domain.RecallConfig{
		BudgetFunction:        domain.BudgetFunctionFixed,
		BudgetLow:             ptr(100),
		BudgetMid:             ptr(300),
		BudgetHigh:            ptr(1000),
		RerankerMaxCandidates: ptr(300),
		Rerank:                ptr(true),
		RRFK:                  ptr(60),
		RecencyAlpha:          ptr(0.2),
		TemporalAlpha:         ptr(0.2),
		ProofAlpha:            ptr(0.1),
		BudgetMin:             ptr(20),
		BudgetMax:             ptr(2000),
	}
}

func reflectDefaults() *domain.ReflectConfig {
	return &domain.ReflectConfig{
		MaxIterations:                     ptr(10),
		SearchObservationsMaxTokens:       ptr(5000),
		SearchObservationsIncludeEntities: ptr(true),
		MaxAnswerTokens:                   ptr(4096),
	}
}

func mentalModelDefaults() *domain.MentalModelConfig {
	return &domain.MentalModelConfig{
		DefaultMode:               domain.RefreshModeFull,
		RefreshDebounceSeconds:    ptr(30),
		MinRefreshIntervalSeconds: ptr(0),
		HistoryMaxEntries:         ptr(50),
		DefaultStrategy:           domain.RefreshStrategyAgentic,
	}
}

func ptr[T any](v T) *T { return &v }
