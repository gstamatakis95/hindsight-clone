package prompts

import (
	"memd/internal/domain"
	"time"
)

// Template names. Render(name, data) executes templates/<name>.tmpl; a structured call's response schema
// is Schema(<its system template name>), so Version of that name covers the schema too. Each template
// family reads one data struct, documented below; the engine fills it once and renders every template
// of the family with it.
const (
	// ExtractConcise is the concise-mode extraction system prompt (the default mode); reads ExtractData.
	ExtractConcise = "extract_concise"
	// ExtractVerbose is the verbose-mode extraction system prompt; reads ExtractData.
	ExtractVerbose = "extract_verbose"
	// ExtractVerbatim is the verbatim-mode extraction system prompt (metadata only, one entry per chunk);
	// reads ExtractData.
	ExtractVerbatim = "extract_verbatim"
	// ExtractCustom is the custom-mode extraction system prompt, built around
	// BankConfig.CustomExtractionPrompt; reads ExtractData.
	ExtractCustom = "extract_custom"
	// ExtractUser is the extraction user message for every mode (mission, date, context, narrator, hints,
	// chunk); reads ExtractData.
	ExtractUser = "extract_user"

	// ConsolidateSystem is the CONSOLIDATE system prompt (create / update decisions); reads ConsolidateData.
	ConsolidateSystem = "consolidate_system"
	// ConsolidateUser is the CONSOLIDATE user message (mission, capacity, facts, observations); reads
	// ConsolidateData.
	ConsolidateUser = "consolidate_user"

	// DedupSystem is the DEDUP_CHECK system prompt (merge-or-keep); reads DedupData.
	DedupSystem = "dedup_system"
	// DedupUser is the DEDUP_CHECK user message (the new and the existing observation); reads DedupData.
	DedupUser = "dedup_user"

	// ReflectSystem is the system prompt of the reflect tool-calling loop; reads ReflectData.
	ReflectSystem = "reflect_system"
	// ReflectDoneRequest is the closing user turn of the loop asking for the answer as a done call; reads
	// ReflectData.
	ReflectDoneRequest = "reflect_done_request"
	// ReflectFinal is the system prompt of the tool-less final-answer call; reads ReflectData.
	ReflectFinal = "reflect_final"
	// ReflectFinalUser is the user message of the tool-less final-answer call (retrieved data and the
	// question); reads ReflectData.
	ReflectFinalUser = "reflect_final_user"

	// StructuredOutputSystem is the STRUCTURED_OUTPUT system prompt; reads StructuredOutputData. The
	// response schema is the caller's, not an embedded one.
	StructuredOutputSystem = "structured_output_system"
	// StructuredOutputUser is the STRUCTURED_OUTPUT user message (document, fields, schema); reads
	// StructuredOutputData.
	StructuredOutputUser = "structured_output_user"

	// SynthesizeSystem is the SYNTHESIZE system prompt (single-shot full page); reads SynthesizeData.
	SynthesizeSystem = "synthesize_system"
	// SynthesizeUser is the SYNTHESIZE user message (topic, observations, facts); reads SynthesizeData.
	SynthesizeUser = "synthesize_user"

	// MentalModelStructuredDeltaSystem is the MENTAL_MODEL_DELTA system prompt (edit ops over sections
	// and blocks); reads DeltaData.
	MentalModelStructuredDeltaSystem = "mm_structured_delta_system"
	// MentalModelStructuredDeltaUser is the MENTAL_MODEL_DELTA user message (document JSON, new facts,
	// budgets, retry feedback); reads DeltaData.
	MentalModelStructuredDeltaUser = "mm_structured_delta_user"
	// MentalModelRefreshContext is the reflect context an agentic mental-model refresh passes; reads
	// RefreshContextData.
	MentalModelRefreshContext = "mm_refresh_context"

	// ReflectToolErrorSearchFirst is the tool error a done call gets before any retrieval, naming the offered
	// search tools; reads ReflectData.
	ReflectToolErrorSearchFirst = "reflect_tool_error_search_first"
	// ReflectToolErrorUnknown is the instruction after a call to a tool that isn't offered, listing the offered
	// tools; reads ReflectData. The engine prefixes the rejected tool's name.
	ReflectToolErrorUnknown = "reflect_tool_error_unknown"
	// ReflectToolErrorDone is the retry instruction after a malformed done call; reads ReflectData. The engine
	// prefixes what was wrong.
	ReflectToolErrorDone = "reflect_tool_error_done"

	// ReflectToolDirectiveRules is the numbered directive rule list DoneTool puts into the directive-aware
	// done tools; reads ReflectData. Empty when no directive has rules.
	ReflectToolDirectiveRules = "reflect_tool_directive_rules"
)

// JSON asset names, read with JSON(name); the done variants are read with DoneTool(name, data).
const (
	// ReflectTools is the reflect tool list: an array of {"name","description","parameters"} for
	// search_mental_models, read_mental_models, search_observations, recall, expand and done.
	ReflectTools = "reflect_tools"
	// ReflectToolDoneDirectives is the done tool ({"name","description","parameters"}) that replaces
	// the plain done when directives apply; it adds a required directive_compliance argument. Its
	// descriptions hold a {directive_rules} slot, so read it with DoneTool, which fills in the rules.
	ReflectToolDoneDirectives = "reflect_tool_done_directives"
	// ReflectToolDoneDocument is the done tool that replaces the plain done when the answer is stored
	// as a document (agentic mental-model refresh); it takes a structured "document" instead of "answer".
	ReflectToolDoneDocument = "reflect_tool_done_document"
	// ReflectToolDoneDocumentDirectives is the document-mode done tool for when directives apply: a
	// structured "document" plus the required directive_compliance argument. Read it with DoneTool.
	ReflectToolDoneDocumentDirectives = "reflect_tool_done_document_directives"
)

// ExtractData is read by ExtractConcise, ExtractVerbose, ExtractVerbatim, ExtractCustom and ExtractUser.
type ExtractData struct {
	// CustomPrompt is BankConfig.CustomExtractionPrompt, the guidelines block of ExtractCustom.
	CustomPrompt string `json:"custom_prompt"`
	// CausalLinks is RetainTuning.ExtractCausalLinks; true adds the causal-relations section. When false
	// the section is omitted (as in Hindsight) and the engine ignores the schema's caused_by.
	CausalLinks bool `json:"causal_links"`
	// OptionalDimensions is Hindsight's retain_optional_fact_dimensions (default off; memd has no knob yet).
	// False asks for "N/A" in empty when/where/who/why; true asks for null and adds the OPTIONAL FIELDS
	// section. The schemas type those fields as string-or-null either way, so only the prompt changes;
	// the engine treats "N/A" and null alike.
	OptionalDimensions bool `json:"optional_dimensions"`
	// AgentName is the narrator, the AI agent whose memory this is; non-empty adds Hindsight's narrator
	// rule (first-person statements are the agent's own, fact_type "assistant") to ExtractUser.
	AgentName string `json:"agent_name"`
	// FreeFormEntities is BankConfig.EntitiesAllowFreeForm resolved (nil = true); false with labels set
	// is labels-only mode. It only matters when Labels is non-empty.
	FreeFormEntities bool `json:"free_form_entities"`
	// Labels are BankConfig.EntityLabels, rendered with their allowed values and descriptions.
	Labels []domain.EntityLabel `json:"labels"`
	// Mission is BankConfig.RetainMission; empty = no focus block.
	Mission string `json:"mission"`
	// MentionedAt is the item timestamp, the reference for relative dates; nil = unknown.
	MentionedAt *time.Time `json:"mentioned_at"`
	// Context is the retain item's free-form context; empty = none.
	Context string `json:"context"`
	// Metadata is the retain item's string metadata (rendered in key order).
	Metadata map[string]string `json:"metadata"`
	// Hints are caller-supplied entities the extractor should name consistently.
	Hints []domain.EntityHint `json:"hints"`
	// Text is the chunk text.
	Text string `json:"text"`
}

// ConsolidateData is read by ConsolidateSystem and ConsolidateUser.
type ConsolidateData struct {
	// Mission is the effective observations mission (strategy over bank); empty = the built-in default.
	Mission string `json:"mission"`
	// Capacity is the scope's observation cap; nil = uncapped.
	Capacity *ConsolidateCapacity `json:"capacity"`
	// Facts are the new facts of this batch.
	Facts []SourceFact `json:"facts"`
	// Observations are the existing observations pooled from recalls across the new facts.
	Observations []ConsolidateObservation `json:"observations"`
}

// ConsolidateCapacity is the observation cap of a scope (max_observations_per_scope).
type ConsolidateCapacity struct {
	// Max is the cap.
	Max int `json:"max"`
	// Remaining is how many observations the scope may still create.
	Remaining int `json:"remaining"`
}

// SourceFact is one fact or observation shown to a prompt with its id (consolidation's new facts,
// synthesize and the deltas).
type SourceFact struct {
	// ID is the unit id the model copies into its response.
	ID string `json:"id"`
	// Type is world, experience or observation; empty = not shown.
	Type string `json:"type,omitempty"`
	// Text is the unit text.
	Text string `json:"text"`
	// Context is the retain item's context, if any.
	Context string `json:"context,omitempty"`
	// OccurredStart is when the event started; nil = unknown.
	OccurredStart *time.Time `json:"occurred_start,omitempty"`
	// OccurredEnd is when the event ended; nil = unknown.
	OccurredEnd *time.Time `json:"occurred_end,omitempty"`
	// MentionedAt is when the source stated the fact; nil = unknown.
	MentionedAt *time.Time `json:"mentioned_at,omitempty"`
}

// ConsolidateObservation is one existing observation shown to CONSOLIDATE (rendered as JSON).
type ConsolidateObservation struct {
	// ID is the observation id the model copies into an update.
	ID string `json:"id"`
	// Text is the observation text.
	Text string `json:"text"`
	// ProofCount is how many source facts the observation has merged.
	ProofCount int `json:"proof_count"`
	// OccurredStart is the earliest start across its sources; nil = unknown.
	OccurredStart *time.Time `json:"occurred_start,omitempty"`
	// OccurredEnd is the latest end across its sources; nil = unknown.
	OccurredEnd *time.Time `json:"occurred_end,omitempty"`
	// MentionedAt is the latest mention across its sources; nil = unknown.
	MentionedAt *time.Time `json:"mentioned_at,omitempty"`
	// SourceMemories are some of its source facts (bounded by the per-observation token cap).
	SourceMemories []SourceMemory `json:"source_memories,omitempty"`
}

// SourceMemory is one source fact of an existing observation as CONSOLIDATE shows it (rendered as JSON).
// Like Hindsight's _build_observations_for_llm it carries no id: source_fact_ids may only name the new
// facts of the batch, so the model is never shown an id it must not copy.
type SourceMemory struct {
	// Text is the unit text.
	Text string `json:"text"`
	// Context is the retain item's context, if any.
	Context string `json:"context,omitempty"`
	// OccurredStart is when the event started; nil = unknown.
	OccurredStart *time.Time `json:"occurred_start,omitempty"`
	// OccurredEnd is when the event ended; nil = unknown.
	OccurredEnd *time.Time `json:"occurred_end,omitempty"`
	// MentionedAt is when the source stated the fact; nil = unknown.
	MentionedAt *time.Time `json:"mentioned_at,omitempty"`
}

// DedupData is read by DedupSystem and DedupUser.
type DedupData struct {
	// New is the text of the observation about to be stored.
	New string `json:"new"`
	// Existing is the text of the near-duplicate observation already in the scope.
	Existing string `json:"existing"`
}

// ReflectData is read by ReflectSystem, ReflectDoneRequest, ReflectFinal, ReflectFinalUser and
// ReflectToolDirectiveRules.
type ReflectData struct {
	// BankName is the bank's display name; empty = not shown.
	BankName string `json:"bank_name"`
	// Mission is BankConfig.ReflectMission; empty = the built-in role.
	Mission string `json:"mission"`
	// Disposition is BankConfig.Disposition; nil = not shown. A 0 trait is unset and is not listed.
	Disposition *domain.Disposition `json:"disposition"`
	// Directives are the active directives matching the request (rendered as Name + Rules).
	Directives []domain.Directive `json:"directives"`
	// Context is additional context for the agent (e.g. a mental-model refresh's instructions).
	Context string `json:"context"`
	// SearchMentalModels reports that search_mental_models is offered (the mental-model level).
	SearchMentalModels bool `json:"search_mental_models"`
	// ReadMentalModels reports that read_mental_models is offered.
	ReadMentalModels bool `json:"read_mental_models"`
	// Observations reports that search_observations is offered.
	Observations bool `json:"observations"`
	// Expand reports that expand is offered.
	Expand bool `json:"expand"`
	// Recall reports that recall is offered (the raw-facts level and every recall() instruction).
	Recall bool `json:"recall"`
	// Budget is low, mid or high; empty = no research-depth section.
	Budget string `json:"budget"`
	// AnswerAsDocument has done take a structured document (pair with ReflectToolDoneDocument).
	AnswerAsDocument bool `json:"answer_as_document"`
	// Now is the current time from clock.Clock, rendered at minute precision in UTC.
	Now time.Time `json:"now"`
	// Query is the question.
	Query string `json:"query"`
	// Retrieved are the tool results the final answer is synthesised from, oldest first.
	Retrieved []ToolOutput `json:"retrieved"`
	// Omitted reports that earlier tool results were dropped to fit the context window.
	Omitted bool `json:"omitted"`
	// MaxTokens is the soft visible-length target of the answer; 0 = none.
	MaxTokens int `json:"max_tokens"`
}

// ToolOutput is one tool result shown to the final-answer call.
type ToolOutput struct {
	// Tool is the tool name.
	Tool string `json:"tool"`
	// Output is the tool's JSON output, compact.
	Output string `json:"output"`
}

// StructuredOutputData is read by StructuredOutputSystem and StructuredOutputUser.
type StructuredOutputData struct {
	// Document is the finished answer or mental-model content to extract from.
	Document string `json:"document"`
	// Fields are the schema's top-level properties, in schema order.
	Fields []SchemaField `json:"fields"`
	// Schema is the caller's JSON Schema, pretty-printed.
	Schema string `json:"schema"`
}

// SchemaField is one top-level property of a caller's JSON Schema.
type SchemaField struct {
	// Name is the property name.
	Name string `json:"name"`
	// Type is the JSON type (string, number, integer, boolean, array, object).
	Type string `json:"type"`
	// Required reports whether the schema requires the property.
	Required bool `json:"required"`
	// Description is the property's description, if any.
	Description string `json:"description"`
}

// SynthesizeData is read by SynthesizeSystem and SynthesizeUser.
type SynthesizeData struct {
	// Name is the mental model's name.
	Name string `json:"name"`
	// SourceQuery is the question the page answers.
	SourceQuery string `json:"source_query"`
	// Mission is BankConfig.ReflectMission; empty = the built-in role.
	Mission string `json:"mission"`
	// Directives are the active directives for the model's tags.
	Directives []domain.Directive `json:"directives"`
	// Observations are the retrieved observations (shown first).
	Observations []SourceFact `json:"observations"`
	// Facts are the retrieved raw facts.
	Facts []SourceFact `json:"facts"`
	// MaxTokens is the target page length; 0 = none.
	MaxTokens int `json:"max_tokens"`
	// Now is the current time from clock.Clock.
	Now time.Time `json:"now"`
}

// DeltaData is read by MentalModelStructuredDeltaSystem and MentalModelStructuredDeltaUser.
type DeltaData struct {
	// Name is the mental model's name.
	Name string `json:"name"`
	// SourceQuery is the question the page answers (the TOPIC).
	SourceQuery string `json:"source_query"`
	// Document is the current content split into sections and blocks with stable ids.
	Document DeltaDocument `json:"document"`
	// Observations are the observations new since the last refresh (shown first).
	Observations []SourceFact `json:"observations"`
	// Facts are the raw facts new since the last refresh.
	Facts []SourceFact `json:"facts"`
	// MaxOutputTokens is the response cap the op list must fit in; 0 = not shown.
	MaxOutputTokens int `json:"max_output_tokens"`
	// DocumentTokens is the document's current size; with DocumentBudget it drives the budget note.
	DocumentTokens int `json:"document_tokens"`
	// DocumentBudget is the page's max tokens; 0 = no budget note.
	DocumentBudget int `json:"document_budget"`
	// Truncated reports that the document or facts were cut to fit the context window.
	Truncated bool `json:"truncated"`
	// Rejected are the ops of the previous attempt that could not be applied (the one retry).
	Rejected []RejectedOp `json:"rejected"`
}

// DeltaDocument is a markdown document as sections of blocks, as the structured delta sees it.
type DeltaDocument struct {
	// Sections are the document's sections in order.
	Sections []DeltaSection `json:"sections"`
}

// DeltaSection is one section of a DeltaDocument.
type DeltaSection struct {
	// ID is the section's stable id.
	ID string `json:"id"`
	// Heading is the heading text without '#'; empty for an untitled opening section.
	Heading string `json:"heading"`
	// Level is the heading level, 1-6.
	Level int `json:"level"`
	// Blocks are the section's blocks in order.
	Blocks []DeltaBlock `json:"blocks"`
}

// DeltaBlock is one markdown fragment (paragraph, list, table or code fence) of a section.
type DeltaBlock struct {
	// ID is the block's stable id.
	ID string `json:"id"`
	// Text is the block's markdown.
	Text string `json:"text"`
}

// RejectedOp is one op of a previous delta attempt that was skipped, with why.
type RejectedOp struct {
	// Op is the op as the model sent it, compact JSON.
	Op string `json:"op"`
	// Reason is why it was skipped (e.g. unknown section_id).
	Reason string `json:"reason"`
}

// RefreshContextData is read by MentalModelRefreshContext.
type RefreshContextData struct {
	// Name is the mental model's name.
	Name string `json:"name"`
	// Delta reports a delta refresh (only new facts are visible) rather than a full one.
	Delta bool `json:"delta"`
}
