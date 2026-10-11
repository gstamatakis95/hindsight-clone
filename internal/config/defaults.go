package config

import (
	"errors"
	"fmt"
	"memd/internal/domain"
)

// DefaultEmbedModel is the embedding model when the server config binds none (768 dimensions).
const DefaultEmbedModel = "nomic-embed-text-v1.5"

// ServerDefaults is the server level of the config hierarchy: every default the spec states, with the server
// config file's defaults block (file) merged over them. Knobs whose default the spec leaves open stay unset;
// the engine that reads them owns that default. Models come only from file: a chat-model slot with neither its
// own binding nor models.default stays unbound, and ai.Router rejects calls on it. EMBED defaults to
// DefaultEmbedModel. Without a RERANK binding recall.rerank defaults to false (recall skips the cross-encoder);
// file setting recall.rerank true without one is an error. The result is checked with ValidateServer.
func ServerDefaults(file domain.BankConfig) (domain.BankConfig, error) {
	cfg := Merge(specDefaults(), file)
	if cfg.Models == nil {
		cfg.Models = &domain.ModelBindings{}
	}
	if cfg.Models.Embed == nil {
		cfg.Models.Embed = &domain.ModelBinding{Model: DefaultEmbedModel}
	}
	if _, ok := cfg.Binding(domain.ModelSlotRerank); !ok {
		if file.Recall != nil && file.Recall.Rerank != nil && *file.Recall.Rerank {
			return domain.BankConfig{}, errors.New("config: server defaults: recall.rerank is true but " +
				"models.rerank binds no model")
		}
		cfg.Recall.Rerank = ptr(false)
	}
	if err := ValidateServer(cfg); err != nil {
		return domain.BankConfig{}, fmt.Errorf("config: server defaults: %w", err)
	}
	return cfg, nil
}

// specDefaults holds the defaults stated in the spec's BankConfig comments.
func specDefaults() domain.BankConfig {
	return domain.BankConfig{
		Retain: &domain.RetainConfig{
			ExtractionMode:           domain.ExtractionModeConcise,
			EntitiesAllowFreeForm:    ptr(true),
			DefaultObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModeCombined, MaxCombinations: ptr(15)},
			TemporalLinkCap:          ptr(20),
			EntityMatchThreshold:     ptr(0.6),
		},
		Observations: &domain.ObservationConfig{
			Enabled:                            ptr(true),
			AutoConsolidation:                  ptr(true),
			DedupThreshold:                     ptr(0.97),
			SourceFactsMaxTokensPerObservation: ptr(256),
			LLMBatchSize:                       ptr(8),
			MaxUnitsPerRun:                     ptr(100),
			ReconcileGraceSeconds:              ptr(600),
		},
		Recall:       recallDefaults(),
		Reflect:      reflectDefaults(),
		MentalModels: mentalModelDefaults(),
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
		Disposition:                       &domain.Disposition{Skepticism: 3, Literalism: 3, Empathy: 3},
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
