package domain

import (
	"encoding/json"
	"time"
)

// RefreshMode is full (rewrite) or delta (edit ops over what changed).
type RefreshMode string

// Refresh modes, mirroring memory.v1.RefreshMode.
const (
	// RefreshModeUnspecified is the zero value (inherit).
	RefreshModeUnspecified RefreshMode = ""
	// RefreshModeFull rewrites the document from everything in scope.
	RefreshModeFull RefreshMode = "full"
	// RefreshModeDelta applies edit ops for what changed since last_seq_seen.
	RefreshModeDelta RefreshMode = "delta"
)

// RefreshStrategy picks the Synthesizer.
type RefreshStrategy string

// Refresh strategies, mirroring memory.v1.RefreshStrategy.
const (
	// RefreshStrategyUnspecified is the zero value (inherit; default agentic).
	RefreshStrategyUnspecified RefreshStrategy = ""
	// RefreshStrategyAgentic runs the reflect loop answering as a document (Hindsight's behaviour).
	RefreshStrategyAgentic RefreshStrategy = "agentic"
	// RefreshStrategySingleShot runs one recall and one synthesize or delta call.
	RefreshStrategySingleShot RefreshStrategy = "single_shot"
)

// RefreshOutcome is how a refresh ended.
type RefreshOutcome string

// Refresh outcomes, mirroring memory.v1.RefreshOutcome.
const (
	// RefreshOutcomeUnspecified is the zero value.
	RefreshOutcomeUnspecified RefreshOutcome = ""
	// RefreshOutcomeContentWritten wrote a new version.
	RefreshOutcomeContentWritten RefreshOutcome = "content_written"
	// RefreshOutcomeNoNewFacts found nothing in scope or window; no LLM call was made.
	RefreshOutcomeNoNewFacts RefreshOutcome = "no_new_facts"
	// RefreshOutcomeFailed failed; the document stays as it was.
	RefreshOutcomeFailed RefreshOutcome = "failed"
)

// Detail selects how much of a mental model a read returns.
type Detail string

// Detail levels, mirroring memory.v1.Detail.
const (
	// DetailUnspecified is the zero value (RPC default).
	DetailUnspecified Detail = ""
	// DetailMetadata returns the spec and state, no content.
	DetailMetadata Detail = "metadata"
	// DetailContent adds content and structured output.
	DetailContent Detail = "content"
	// DetailFull adds the last refresh trace.
	DetailFull Detail = "full"
)

// HistoryKind separates version entries from failure entries; each is capped separately.
type HistoryKind string

// History kinds, mirroring memory.v1.HistoryKind.
const (
	// HistoryKindUnspecified is the zero value (all kinds).
	HistoryKindUnspecified HistoryKind = ""
	// HistoryKindVersion records a replaced version's content.
	HistoryKindVersion HistoryKind = "version"
	// HistoryKindRefreshFailed records a failed refresh.
	HistoryKindRefreshFailed HistoryKind = "refresh_failed"
)

// RefreshTrigger says when and how a model refreshes, and over which scope.
type RefreshTrigger struct {
	// Mode is full or delta; unset = MentalModelConfig.DefaultMode.
	Mode RefreshMode `json:"mode,omitempty"`
	// Strategy is agentic or single-shot; unset = MentalModelConfig.DefaultStrategy.
	Strategy RefreshStrategy `json:"strategy,omitempty"`
	// RefreshAfterConsolidation refreshes when consolidation changes the scope (exclusive with RefreshCron).
	RefreshAfterConsolidation bool `json:"refresh_after_consolidation,omitempty"`
	// RefreshCron is a 5-field UTC cron expression (exclusive with RefreshAfterConsolidation).
	RefreshCron string `json:"refresh_cron,omitempty"`
	// MinRefreshIntervalSeconds is the interval floor; nil = MentalModelConfig value.
	MinRefreshIntervalSeconds *int `json:"min_refresh_interval_seconds,omitempty"`
	// TagsMatch applies to the model's tags; unset = all_strict when the model is tagged.
	TagsMatch TagsMatch `json:"tags_match,omitempty"`
	// TagGroups override the model's tags and TagsMatch.
	TagGroups []TagGroup `json:"tag_groups,omitempty"`
	// FactTypes limits the scope; empty = all.
	FactTypes []FactType `json:"fact_types,omitempty"`
	// ExcludeMentalModels hides other models from the agentic refresh.
	ExcludeMentalModels bool `json:"exclude_mental_models,omitempty"`
	// ExcludeMentalModelIDs hides specific models.
	ExcludeMentalModelIDs []MentalModelID `json:"exclude_mental_model_ids,omitempty"`
	// IncludeChunks attaches chunks to recall results.
	IncludeChunks *bool `json:"include_chunks,omitempty"`
	// RecallMaxTokens caps recall results.
	RecallMaxTokens *int `json:"recall_max_tokens,omitempty"`
	// RecallChunksMaxTokens caps attached chunks.
	RecallChunksMaxTokens *int `json:"recall_chunks_max_tokens,omitempty"`
	// SearchObservationsMaxTokens caps search_observations results.
	SearchObservationsMaxTokens *int `json:"search_observations_max_tokens,omitempty"`
	// SearchObservationsIncludeEntities adds entity state to search_observations.
	SearchObservationsIncludeEntities *bool `json:"search_observations_include_entities,omitempty"`
	// Budget is the refresh's budget; unset = mid.
	Budget Budget `json:"budget,omitempty"`
	// ResponseSchemaJSON asks for a structured-output projection of the document.
	ResponseSchemaJSON string `json:"response_schema_json,omitempty"`
	// KeepTrace stores the last refresh trace on the model.
	KeepTrace bool `json:"keep_trace,omitempty"`
}

// MentalModelSpec is the writable part of a mental model.
type MentalModelSpec struct {
	// ID is lowercase alphanumerics and hyphens, unique per bank.
	ID MentalModelID `json:"id"`
	// Name is a display name.
	Name string `json:"name,omitempty"`
	// SourceQuery is the question the document answers.
	SourceQuery string `json:"source_query,omitempty"`
	// FolderPath places the page in a folder tree.
	FolderPath string `json:"folder_path,omitempty"`
	// Tags scope the model.
	Tags []string `json:"tags,omitempty"`
	// MaxTokens overrides MentalModelConfig.MaxContentTokens.
	MaxTokens *int `json:"max_tokens,omitempty"`
	// Trigger is the refresh trigger and scope.
	Trigger RefreshTrigger `json:"trigger"`
	// Overrides are applied to every refresh.
	Overrides *BankConfig `json:"overrides,omitempty"`
}

// MentalModel is a synthesized page with its refresh state.
type MentalModel struct {
	// Spec is the writable part.
	Spec MentalModelSpec
	// Bank owns the model.
	Bank BankRef
	// ContentMD is the current markdown document.
	ContentMD string
	// StructuredOutputJSON is the structured projection, if a schema is set.
	StructuredOutputJSON json.RawMessage
	// Version is bumped on every written version.
	Version int64
	// IsStale is computed by readers from the change detector.
	IsStale bool
	// LastRefreshedAt is the wall clock of the last completed refresh.
	LastRefreshedAt *time.Time
	// LastSeqSeen is the bank change_seq the last successful refresh read up to.
	LastSeqSeen int64
	// LastRefreshFailedAt is when the last refresh failed.
	LastRefreshFailedAt *time.Time
	// AutoRefreshPaused is set by a failed refresh; only a manual success clears it.
	AutoRefreshPaused bool
	// SourceIDs is the cumulative based_on set; pruned on full refresh and invalidate.
	SourceIDs []UnitID
	// LastTrace is kept when the trigger asks for it.
	LastTrace *RefreshTrace
	// Embedding is the page embedding used by page search.
	Embedding []float32
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// ToolCallTrace records one tool call of a reflect loop or agentic refresh.
type ToolCallTrace struct {
	// Iteration is the loop iteration, from 1.
	Iteration int `json:"iteration"`
	// Tool is the tool name.
	Tool string `json:"tool"`
	// ArgumentsJSON is the call's arguments.
	ArgumentsJSON string `json:"arguments_json,omitempty"`
	// OutputJSON is the tool output (only when asked for).
	OutputJSON string `json:"output_json,omitempty"`
	// Error is the tool error, if any.
	Error string `json:"error,omitempty"`
	// DurationMS is the call's duration.
	DurationMS int64 `json:"duration_ms"`
}

// DeltaOpKind names one edit operation of a delta refresh.
type DeltaOpKind string

// Delta op kinds, mirroring memory.v1.DeltaOp.Kind.
const (
	// DeltaOpAddSection adds a section.
	DeltaOpAddSection DeltaOpKind = "add_section"
	// DeltaOpRemoveSection removes a section.
	DeltaOpRemoveSection DeltaOpKind = "remove_section"
	// DeltaOpRenameSection renames a section heading.
	DeltaOpRenameSection DeltaOpKind = "rename_section"
	// DeltaOpReplaceSectionBlocks replaces all blocks of a section.
	DeltaOpReplaceSectionBlocks DeltaOpKind = "replace_section_blocks"
	// DeltaOpAppendBlock appends a block to a section.
	DeltaOpAppendBlock DeltaOpKind = "append_block"
	// DeltaOpInsertBlock inserts a block after another.
	DeltaOpInsertBlock DeltaOpKind = "insert_block"
	// DeltaOpReplaceBlock replaces one block.
	DeltaOpReplaceBlock DeltaOpKind = "replace_block"
	// DeltaOpRemoveBlock removes one block.
	DeltaOpRemoveBlock DeltaOpKind = "remove_block"
)

// DeltaOp is one edit op as proposed and as applied (or skipped, with the reason).
type DeltaOp struct {
	// Kind is the op.
	Kind DeltaOpKind `json:"kind"`
	// SectionID anchors section ops.
	SectionID string `json:"section_id,omitempty"`
	// BlockID anchors block ops.
	BlockID string `json:"block_id,omitempty"`
	// AfterBlockID anchors inserts.
	AfterBlockID string `json:"after_block_id,omitempty"`
	// Heading is the new or renamed heading.
	Heading string `json:"heading,omitempty"`
	// BlocksMD are the new blocks' markdown.
	BlocksMD []string `json:"blocks_md,omitempty"`
	// Applied reports whether the op was applied.
	Applied bool `json:"applied"`
	// SkipReason says why it was not.
	SkipReason string `json:"skip_reason,omitempty"`
}

// RefreshTrace explains one refresh.
type RefreshTrace struct {
	// EffectiveMode is the mode that ran.
	EffectiveMode RefreshMode `json:"effective_mode"`
	// ModeFallbackReason explains a delta that ran as full (no_baseline_content, source_query_changed, ...).
	ModeFallbackReason string `json:"mode_fallback_reason,omitempty"`
	// Outcome is how the refresh ended.
	Outcome RefreshOutcome `json:"outcome"`
	// ToolCalls are the agentic loop's calls.
	ToolCalls []ToolCallTrace `json:"tool_calls,omitempty"`
	// DeltaOperations are the delta's ops.
	DeltaOperations []DeltaOp `json:"delta_operations,omitempty"`
	// Usage is per-slot token usage.
	Usage []Usage `json:"usage,omitempty"`
	// RecordedAt is when the trace was recorded.
	RecordedAt time.Time `json:"recorded_at"`
	// DurationMS is the refresh duration.
	DurationMS int64 `json:"duration_ms"`
	// Warnings are non-fatal issues.
	Warnings []string `json:"warnings,omitempty"`
	// EffectiveStrategy is the strategy that ran.
	EffectiveStrategy RefreshStrategy `json:"effective_strategy"`
}

// MentalModelHistoryEntry is one version or failure in a model's history.
type MentalModelHistoryEntry struct {
	// Kind is version or refresh_failed.
	Kind HistoryKind
	// Version is the version that was replaced (version entries).
	Version int64
	// PreviousContent is the replaced content (version entries).
	PreviousContent string
	// ChangedAt is when the entry was written.
	ChangedAt time.Time
	// Outcome is the refresh outcome.
	Outcome RefreshOutcome
	// FailureReason classifies a failure.
	FailureReason string
	// ErrorMessage is the failure's error text.
	ErrorMessage string
}

// PageHit is one SearchPages result.
type PageHit struct {
	// MentalModelID is the page.
	MentalModelID MentalModelID
	// FolderPath is the page's folder.
	FolderPath string
	// Name is the page name.
	Name string
	// Snippet is a matching excerpt.
	Snippet string
	// Score is the fused full-text and semantic score.
	Score float64
}
