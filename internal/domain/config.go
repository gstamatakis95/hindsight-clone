package domain

// ExtractionMode selects how retain turns a document into facts.
type ExtractionMode string

// Extraction modes, mirroring memory.v1.ExtractionMode.
const (
	// ExtractionModeUnspecified is the zero value (inherit).
	ExtractionModeUnspecified ExtractionMode = ""
	// ExtractionModeConcise extracts compact facts (default).
	ExtractionModeConcise ExtractionMode = "concise"
	// ExtractionModeVerbose extracts detailed facts with who / what / when / where / why.
	ExtractionModeVerbose ExtractionMode = "verbose"
	// ExtractionModeVerbatim keeps the source wording.
	ExtractionModeVerbatim ExtractionMode = "verbatim"
	// ExtractionModeChunks stores chunks as units with no LLM call.
	ExtractionModeChunks ExtractionMode = "chunks"
	// ExtractionModeCustom uses BankConfig.CustomExtractionPrompt.
	ExtractionModeCustom ExtractionMode = "custom"
)

// BudgetFunction picks how recall turns a Budget into a candidate limit.
type BudgetFunction string

// Budget functions, mirroring memory.v1.BudgetFunction.
const (
	// BudgetFunctionUnspecified is the zero value (inherit).
	BudgetFunctionUnspecified BudgetFunction = ""
	// BudgetFunctionFixed uses budget_low / budget_mid / budget_high as is.
	BudgetFunctionFixed BudgetFunction = "fixed"
	// BudgetFunctionAdaptive scales with max_tokens, clamped to [budget_min, budget_max].
	BudgetFunctionAdaptive BudgetFunction = "adaptive"
)

// Budget is the coarse effort level of a recall or reflect call.
type Budget string

// Budgets, mirroring memory.v1.Budget.
const (
	// BudgetUnspecified is the zero value (RPC default applies).
	BudgetUnspecified Budget = ""
	// BudgetLow is the cheapest level (100 candidates per arm by default).
	BudgetLow Budget = "low"
	// BudgetMid is the default level (300 candidates per arm by default).
	BudgetMid Budget = "mid"
	// BudgetHigh is the most thorough level (1000 candidates per arm by default).
	BudgetHigh Budget = "high"
)

// Disposition shapes reflect's tone; each trait is 1-5, 0 = unset (default 3).
type Disposition struct {
	// Skepticism is how much evidence the agent demands.
	Skepticism int `json:"skepticism,omitempty"`
	// Literalism is how literally the agent reads the question.
	Literalism int `json:"literalism,omitempty"`
	// Empathy is how much the agent weighs the user's perspective.
	Empathy int `json:"empathy,omitempty"`
}

// LabelType is the shape of an entity label's values.
type LabelType string

// Label types, mirroring memory.v1.EntityLabel.Type.
const (
	// LabelTypeUnspecified is the zero value (defaults to value).
	LabelTypeUnspecified LabelType = ""
	// LabelTypeValue is one value from a fixed list.
	LabelTypeValue LabelType = "value"
	// LabelTypeMultiValues is several values from a fixed list.
	LabelTypeMultiValues LabelType = "multi_values"
	// LabelTypeText is one free-text value.
	LabelTypeText LabelType = "text"
	// LabelTypeMultiText is several free-text values.
	LabelTypeMultiText LabelType = "multi_text"
	// LabelTypeMap is a set of named sub-labels, stored as key:field:value.
	LabelTypeMap LabelType = "map"
)

// EntityLabel declares a typed key:value label that extraction fills and resolution matches exactly.
type EntityLabel struct {
	// Key is the label key.
	Key string `json:"key"`
	// Description tells the extractor what the label means.
	Description string `json:"description,omitempty"`
	// Type is the value shape; unset = value.
	Type LabelType `json:"type,omitempty"`
	// Values lists the allowed values (value / multi_values only).
	Values []LabelValue `json:"values,omitempty"`
	// Fields holds the sub-labels of a map label.
	Fields map[string]EntityLabel `json:"fields,omitempty"`
	// Optional reports whether extraction may omit the label; nil = true.
	Optional *bool `json:"optional,omitempty"`
	// WriteToTags also writes key:value as a tag on the unit.
	WriteToTags bool `json:"write_to_tags,omitempty"`
}

// LabelValue is one allowed value of a label with its description.
type LabelValue struct {
	// Value is the literal value.
	Value string `json:"value"`
	// Description tells the extractor when the value applies.
	Description string `json:"description,omitempty"`
}

// Label is one extracted key:value pair (a map label's field is "key:field").
type Label struct {
	// Key is the label key (or "key:field" for map labels).
	Key string `json:"key"`
	// Value is the extracted value.
	Value string `json:"value"`
}

// ScopePattern matches observation scopes by their tags.
type ScopePattern struct {
	// Tags are the pattern's tags.
	Tags []string `json:"tags,omitempty"`
	// TagsMatch is how Tags compare against a scope's tags.
	TagsMatch TagsMatch `json:"tags_match,omitempty"`
}

// ConsolidationStrategy overrides consolidation settings for the scopes its patterns match.
type ConsolidationStrategy struct {
	// Name identifies the strategy.
	Name string `json:"name"`
	// Scopes are the patterns; the first strategy with a matching pattern wins.
	Scopes []ScopePattern `json:"scopes,omitempty"`
	// ObservationsMission replaces the bank's observations mission for matched scopes.
	ObservationsMission string `json:"observations_mission,omitempty"`
	// MaxObservationsPerScope caps observations per matched scope.
	MaxObservationsPerScope *int `json:"max_observations_per_scope,omitempty"`
	// SourceFactsMaxTokens caps source facts sent per consolidation call.
	SourceFactsMaxTokens *int `json:"source_facts_max_tokens,omitempty"`
	// SourceFactsMaxTokensPerObservation caps source facts shown per observation.
	SourceFactsMaxTokensPerObservation *int `json:"source_facts_max_tokens_per_observation,omitempty"`
	// Models binds CONSOLIDATE / DEDUP_CHECK for matched scopes.
	Models []SlotBinding `json:"models,omitempty"`
}

// RetainTuning holds chunking, entity matching and linking knobs.
type RetainTuning struct {
	// ChunkMaxTokens is the chunk size.
	ChunkMaxTokens *int `json:"chunk_max_tokens,omitempty"`
	// ChunkOverlapTokens is the overlap between chunks.
	ChunkOverlapTokens *int `json:"chunk_overlap_tokens,omitempty"`
	// EntityMatchThreshold is the resolver's merge threshold.
	EntityMatchThreshold *float64 `json:"entity_match_threshold,omitempty"`
	// TemporalLinkWindowHours bounds temporal links.
	TemporalLinkWindowHours *int `json:"temporal_link_window_hours,omitempty"`
	// SemanticLinkK is the semantic neighbours per unit, also the semantic link cap per unit.
	SemanticLinkK *int `json:"semantic_link_k,omitempty"`
	// SemanticLinkMinSimilarity is the cosine floor for semantic links.
	SemanticLinkMinSimilarity *float64 `json:"semantic_link_min_similarity,omitempty"`
	// ExtractCausalLinks enables causal edges between facts of one batch.
	ExtractCausalLinks *bool `json:"extract_causal_links,omitempty"`
	// StructuredChunkMaxTokens keeps a JSONL line or chat turn whole up to this size.
	StructuredChunkMaxTokens *int `json:"structured_chunk_max_tokens,omitempty"`
	// TemporalLinkCap caps temporal links per unit (default 20).
	TemporalLinkCap *int `json:"temporal_link_cap,omitempty"`
}

// RecallConfig holds recall's budgets, boosts and arm settings.
type RecallConfig struct {
	// BudgetFunction picks fixed or adaptive budgets.
	BudgetFunction BudgetFunction `json:"budget_function,omitempty"`
	// BudgetLow is the per-arm limit at BUDGET_LOW (default 100).
	BudgetLow *int `json:"budget_low,omitempty"`
	// BudgetMid is the per-arm limit at BUDGET_MID (default 300).
	BudgetMid *int `json:"budget_mid,omitempty"`
	// BudgetHigh is the per-arm limit at BUDGET_HIGH (default 1000).
	BudgetHigh *int `json:"budget_high,omitempty"`
	// StrategyBoosts weights arms by name in fusion.
	StrategyBoosts map[string]float64 `json:"strategy_boosts"`
	// EnabledStrategies lists the arms to run; nil = all four.
	EnabledStrategies []string `json:"enabled_strategies"`
	// RerankerMaxCandidates is how many fused candidates are reranked (default 300).
	RerankerMaxCandidates *int `json:"reranker_max_candidates,omitempty"`
	// Rerank toggles the cross-encoder.
	Rerank *bool `json:"rerank,omitempty"`
	// RRFK is the reciprocal-rank-fusion constant (default 60).
	RRFK *int `json:"rrf_k,omitempty"`
	// RecencyAlpha is the recency boost weight (default 0.2).
	RecencyAlpha *float64 `json:"recency_alpha,omitempty"`
	// TemporalAlpha is the temporal-proximity boost weight (default 0.2).
	TemporalAlpha *float64 `json:"temporal_alpha,omitempty"`
	// ProofAlpha is the proof-count boost weight (default 0.1).
	ProofAlpha *float64 `json:"proof_alpha,omitempty"`
	// RecencyHorizonDays is the recency decay horizon.
	RecencyHorizonDays *int `json:"recency_horizon_days,omitempty"`
	// GraphMaxHops bounds graph expansion (1-2).
	GraphMaxHops *int `json:"graph_max_hops,omitempty"`
	// TemporalBuckets is the number of buckets the temporal arm spreads hits over.
	TemporalBuckets *int `json:"temporal_buckets,omitempty"`
	// FuzzyTagMinSimilarity is the trigram floor for fuzzy tag leaves.
	FuzzyTagMinSimilarity *float64 `json:"fuzzy_tag_min_similarity,omitempty"`
	// BudgetMin is the adaptive clamp floor (default 20).
	BudgetMin *int `json:"budget_min,omitempty"`
	// BudgetMax is the adaptive clamp ceiling (default 2000).
	BudgetMax *int `json:"budget_max,omitempty"`
}

// ReflectConfig holds the reflect loop's limits and observation-search knobs.
type ReflectConfig struct {
	// MaxIterations at BUDGET_MID (default 10); LOW halves, HIGH doubles.
	MaxIterations *int `json:"max_iterations,omitempty"`
	// EnabledTools lists the tools the agent may call; nil = all.
	EnabledTools []string `json:"enabled_tools"`
	// RequireEvidence forces at least one retrieval before done.
	RequireEvidence *bool `json:"require_evidence,omitempty"`
	// RecallBudget is the budget of the recall tool.
	RecallBudget Budget `json:"recall_budget,omitempty"`
	// RecallMaxTokens caps one recall tool result.
	RecallMaxTokens *int `json:"recall_max_tokens,omitempty"`
	// SearchObservationsMaxTokens caps one search_observations result (default 5000).
	SearchObservationsMaxTokens *int `json:"search_observations_max_tokens,omitempty"`
	// SearchObservationsIncludeEntities adds entity state to search_observations (default true).
	SearchObservationsIncludeEntities *bool `json:"search_observations_include_entities,omitempty"`
	// MaxAnswerTokens caps the final answer only (default 4096).
	MaxAnswerTokens *int `json:"max_answer_tokens,omitempty"`
	// MaxTotalTokens is the whole-call token budget; exceeding it fails with RESOURCE_EXHAUSTED.
	MaxTotalTokens *int64 `json:"max_total_tokens,omitempty"`
}

// MentalModelConfig holds bank-level mental-model defaults; a model's RefreshTrigger wins.
type MentalModelConfig struct {
	// DefaultMode is the refresh mode when a trigger leaves it unset.
	DefaultMode RefreshMode `json:"default_mode,omitempty"`
	// MaxDeltaRatio bounds how much of a document a delta may change before falling back to full.
	MaxDeltaRatio *float64 `json:"max_delta_ratio,omitempty"`
	// RefreshDebounceSeconds delays triggered refreshes so bursts join one (default 30).
	RefreshDebounceSeconds *int `json:"refresh_debounce_seconds,omitempty"`
	// MaxContentTokens caps a model's content.
	MaxContentTokens *int `json:"max_content_tokens,omitempty"`
	// MinRefreshIntervalSeconds is the interval floor for triggered refreshes (default 0 = none).
	MinRefreshIntervalSeconds *int `json:"min_refresh_interval_seconds,omitempty"`
	// HistoryMaxEntries caps history per kind (default 50).
	HistoryMaxEntries *int `json:"history_max_entries,omitempty"`
	// DefaultStrategy is the refresh strategy when unset (default agentic).
	DefaultStrategy RefreshStrategy `json:"default_strategy,omitempty"`
}

// BankConfig is the one configuration shape used at every level:
// server < tenant < bank < strategy < request (< item). Scalars: set replaces unset. Models: merged per
// slot. Other lists: set replaces (no append); a non-nil empty slice clears.
type BankConfig struct {
	// RetainMission steers extraction.
	RetainMission string `json:"retain_mission,omitempty"`
	// ObservationsMission steers consolidation.
	ObservationsMission string `json:"observations_mission,omitempty"`
	// ReflectMission steers reflect.
	ReflectMission string `json:"reflect_mission,omitempty"`
	// Disposition shapes reflect's tone.
	Disposition *Disposition `json:"disposition,omitempty"`
	// ExtractionMode selects the extraction strategy.
	ExtractionMode ExtractionMode `json:"extraction_mode,omitempty"`
	// CustomExtractionPrompt is used by the custom extraction mode.
	CustomExtractionPrompt string `json:"custom_extraction_prompt,omitempty"`
	// EntityLabels declares typed labels.
	EntityLabels []EntityLabel `json:"entity_labels"`
	// AutoConsolidation enqueues consolidation after retain.
	AutoConsolidation *bool `json:"auto_consolidation,omitempty"`
	// DedupThreshold is the cosine floor for near-duplicate observations (default 0.97).
	DedupThreshold *float64 `json:"dedup_threshold,omitempty"`
	// MaxObservationsPerScope caps observations per scope.
	MaxObservationsPerScope *int `json:"max_observations_per_scope,omitempty"`
	// ConsolidationSourceFactsMaxTokens caps source facts per consolidation call.
	ConsolidationSourceFactsMaxTokens *int `json:"consolidation_source_facts_max_tokens,omitempty"`
	// ConsolidationStrategies are per-scope overrides; the first match wins.
	ConsolidationStrategies []ConsolidationStrategy `json:"consolidation_strategies"`
	// Recall holds recall settings.
	Recall *RecallConfig `json:"recall,omitempty"`
	// Models binds slots to models; merged per slot across levels.
	Models []SlotBinding `json:"models,omitempty"`
	// Retain holds retain tuning.
	Retain *RetainTuning `json:"retain,omitempty"`
	// Reflect holds reflect settings.
	Reflect *ReflectConfig `json:"reflect,omitempty"`
	// MentalModels holds mental-model defaults.
	MentalModels *MentalModelConfig `json:"mental_models,omitempty"`
	// DefaultObservationScopes is the scope mode for items that set none.
	DefaultObservationScopes *ObservationScopes `json:"default_observation_scopes,omitempty"`
	// EnableObservations false disables consolidation entirely.
	EnableObservations *bool `json:"enable_observations,omitempty"`
	// ConsolidationLLMBatchSize is facts per consolidation call (default 8).
	ConsolidationLLMBatchSize *int `json:"consolidation_llm_batch_size,omitempty"`
	// ConsolidationSourceTokensPerObs caps source facts per observation (default 256).
	ConsolidationSourceTokensPerObs *int `json:"consolidation_source_facts_max_tokens_per_observation,omitempty"`
	// EntitiesAllowFreeForm false restricts entities to labels.
	EntitiesAllowFreeForm *bool `json:"entities_allow_free_form,omitempty"`
	// ConsolidationMaxUnitsPerRun bounds one consolidation run, then it re-enqueues (default 100).
	ConsolidationMaxUnitsPerRun *int `json:"consolidation_max_units_per_run,omitempty"`
}

// Binding returns the binding for slot from cfg.Models and whether one is set. It does not merge levels.
func (c BankConfig) Binding(slot ModelSlot) (ModelBinding, bool) {
	for _, sb := range c.Models {
		if sb.Slot == slot {
			return sb.Binding, true
		}
	}
	return ModelBinding{}, false
}
