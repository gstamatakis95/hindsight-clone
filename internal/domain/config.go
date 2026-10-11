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

// ConsolidationStrategy overrides consolidation settings for the scopes its patterns match.
type ConsolidationStrategy struct {
	// Name identifies the strategy; unique within one strategy list.
	Name string `json:"name"`
	// Scopes are the patterns, tag filters on a scope's tag set (unset match = all_strict); the first strategy
	// with a matching pattern wins (config.MatchStrategy).
	Scopes []TagFilter `json:"scopes,omitempty"`
	// Mission replaces the observations mission for matched scopes.
	Mission string `json:"mission,omitempty"`
	// MaxObservationsPerScope caps observations per matched scope.
	MaxObservationsPerScope *int `json:"max_observations_per_scope,omitempty"`
	// SourceFactsMaxTokens caps source facts sent per consolidation call.
	SourceFactsMaxTokens *int `json:"source_facts_max_tokens,omitempty"`
	// SourceFactsMaxTokensPerObservation caps source facts shown per observation.
	SourceFactsMaxTokensPerObservation *int `json:"source_facts_max_tokens_per_observation,omitempty"`
	// ConsolidateModel binds the consolidate slot for matched scopes.
	ConsolidateModel *ModelBinding `json:"consolidate_model,omitempty"`
	// DedupCheckModel binds the dedup_check slot for matched scopes.
	DedupCheckModel *ModelBinding `json:"dedup_check_model,omitempty"`
}

// RetainConfig holds everything retain reads: the mission, extraction, labels, default scopes, chunking, entity
// matching and linking.
type RetainConfig struct {
	// Mission steers extraction.
	Mission string `json:"mission,omitempty"`
	// ExtractionMode selects the extraction strategy.
	ExtractionMode ExtractionMode `json:"extraction_mode,omitempty"`
	// CustomExtractionPrompt is used by the custom extraction mode.
	CustomExtractionPrompt string `json:"custom_extraction_prompt,omitempty"`
	// EntityLabels declares typed labels.
	EntityLabels []EntityLabel `json:"entity_labels"`
	// EntitiesAllowFreeForm false restricts entities to labels.
	EntitiesAllowFreeForm *bool `json:"entities_allow_free_form,omitempty"`
	// DefaultObservationScopes is the scope mode for items that set none.
	DefaultObservationScopes *ObservationScopes `json:"default_observation_scopes,omitempty"`
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

// ObservationConfig holds consolidation: whether it runs, its mission, its limits and its per-scope strategies.
type ObservationConfig struct {
	// Enabled false disables consolidation entirely.
	Enabled *bool `json:"enabled,omitempty"`
	// AutoConsolidation enqueues consolidation after retain and lets the maintenance tick catch up.
	AutoConsolidation *bool `json:"auto_consolidation,omitempty"`
	// Mission steers consolidation.
	Mission string `json:"mission,omitempty"`
	// DedupThreshold is the cosine floor for near-duplicate observations (default 0.97).
	DedupThreshold *float64 `json:"dedup_threshold,omitempty"`
	// MaxObservationsPerScope caps observations per scope.
	MaxObservationsPerScope *int `json:"max_observations_per_scope,omitempty"`
	// SourceFactsMaxTokens caps source facts per consolidation call.
	SourceFactsMaxTokens *int `json:"source_facts_max_tokens,omitempty"`
	// SourceFactsMaxTokensPerObservation caps source facts shown per observation (default 256).
	SourceFactsMaxTokensPerObservation *int `json:"source_facts_max_tokens_per_observation,omitempty"`
	// LLMBatchSize is facts per consolidation call (default 8).
	LLMBatchSize *int `json:"llm_batch_size,omitempty"`
	// MaxUnitsPerRun bounds one consolidation run, then it re-enqueues (default 100).
	MaxUnitsPerRun *int `json:"max_units_per_run,omitempty"`
	// Strategies are per-scope overrides; the first match wins.
	Strategies []ConsolidationStrategy `json:"strategies"`
	// ReconcileGraceSeconds is how old an unconsolidated fact must be before the maintenance tick consolidates
	// its bank (default 600).
	ReconcileGraceSeconds *int `json:"reconcile_grace_seconds,omitempty"`
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
	// ArmBoosts weights recall arms by name in fusion.
	ArmBoosts map[string]float64 `json:"arm_boosts"`
	// Arms lists the recall arms to run by name; empty = every registered arm.
	Arms []string `json:"arms"`
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

// ReflectConfig holds reflect's mission and disposition, the loop's limits and its observation-search knobs.
type ReflectConfig struct {
	// Mission steers reflect.
	Mission string `json:"mission,omitempty"`
	// Disposition shapes reflect's tone.
	Disposition *Disposition `json:"disposition,omitempty"`
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

// MentalModelConfig holds bank-level mental-model defaults; a model's MentalModelSpec wins.
type MentalModelConfig struct {
	// DefaultMode is the refresh mode when the spec leaves it unset.
	DefaultMode RefreshMode `json:"default_mode,omitempty"`
	// MaxDeltaRatio bounds how much of a document a delta may change before falling back to full.
	MaxDeltaRatio *float64 `json:"max_delta_ratio,omitempty"`
	// RefreshDebounceSeconds delays automatic refreshes so bursts join one (default 30).
	RefreshDebounceSeconds *int `json:"refresh_debounce_seconds,omitempty"`
	// MaxContentTokens caps a model's content.
	MaxContentTokens *int `json:"max_content_tokens,omitempty"`
	// MinRefreshIntervalSeconds is the interval floor for automatic refreshes (default 0 = none).
	MinRefreshIntervalSeconds *int `json:"min_refresh_interval_seconds,omitempty"`
	// HistoryMaxEntries caps history per kind (default 50).
	HistoryMaxEntries *int `json:"history_max_entries,omitempty"`
	// DefaultStrategy is the refresh strategy when unset (default agentic).
	DefaultStrategy RefreshStrategy `json:"default_strategy,omitempty"`
}

// BankConfig is the one configuration shape used at every level, grouped by phase:
// server < tenant < bank < strategy < request (< item). Scalars: set replaces unset. Groups merge field by field.
// A ModelBinding is atomic. Lists: set replaces (no append); a non-nil empty slice clears.
type BankConfig struct {
	// Retain holds extraction, labels, chunking and linking.
	Retain *RetainConfig `json:"retain,omitempty"`
	// Observations holds consolidation.
	Observations *ObservationConfig `json:"observations,omitempty"`
	// Recall holds recall settings.
	Recall *RecallConfig `json:"recall,omitempty"`
	// Reflect holds reflect settings.
	Reflect *ReflectConfig `json:"reflect,omitempty"`
	// MentalModels holds mental-model defaults.
	MentalModels *MentalModelConfig `json:"mental_models,omitempty"`
	// Models binds model slots to gateway models.
	Models *ModelBindings `json:"models,omitempty"`
}

// GetRetain returns the retain group, or the zero group when c leaves it unset.
func (c BankConfig) GetRetain() RetainConfig {
	if c.Retain == nil {
		return RetainConfig{}
	}
	return *c.Retain
}

// GetObservations returns the observations group, or the zero group when c leaves it unset.
func (c BankConfig) GetObservations() ObservationConfig {
	if c.Observations == nil {
		return ObservationConfig{}
	}
	return *c.Observations
}

// GetReflect returns the reflect group, or the zero group when c leaves it unset.
func (c BankConfig) GetReflect() ReflectConfig {
	if c.Reflect == nil {
		return ReflectConfig{}
	}
	return *c.Reflect
}

// Binding returns the binding c.Models yields for slot (ModelBindings.For) and whether it names a model. It reads
// one level, or an already-merged config; it does not merge levels.
func (c BankConfig) Binding(slot ModelSlot) (ModelBinding, bool) {
	b := c.Models.For(slot)
	if b == nil || b.Model == "" {
		return ModelBinding{}, false
	}
	return *b, true
}
