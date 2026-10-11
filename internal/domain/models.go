package domain

// ModelSlot names one model-backed step; each slot binds to its own gateway model and parameters.
type ModelSlot string

// Model slots, mirroring memory.v1.ModelSlot.
const (
	// ModelSlotUnspecified is the zero value.
	ModelSlotUnspecified ModelSlot = ""
	// ModelSlotExtract is retain fact, entity and label extraction.
	ModelSlotExtract ModelSlot = "extract"
	// ModelSlotTimeParse resolves relative dates at retain and query time windows when rules fail.
	ModelSlotTimeParse ModelSlot = "time_parse"
	// ModelSlotConsolidate makes create / update / none decisions per scope.
	ModelSlotConsolidate ModelSlot = "consolidate"
	// ModelSlotDedupCheck is the merge-or-keep check for near-duplicate observations.
	ModelSlotDedupCheck ModelSlot = "dedup_check"
	// ModelSlotReflect drives the tool-calling turns of reflect and agentic refresh.
	ModelSlotReflect ModelSlot = "reflect"
	// ModelSlotStructuredOutput projects prose into a JSON Schema.
	ModelSlotStructuredOutput ModelSlot = "structured_output"
	// ModelSlotMentalModel is the single-shot mental-model refresh: the full page synthesis and the delta edit ops.
	ModelSlotMentalModel ModelSlot = "mental_model"
	// ModelSlotRerank is the cross-encoder used by recall.
	ModelSlotRerank ModelSlot = "rerank"
	// ModelSlotEmbed is the embedding model; it is server-wide (one vector space per database).
	ModelSlotEmbed ModelSlot = "embed"
)

// LLMSlots are the chat-model slots, the ones ModelBindings.Default fills. RERANK and EMBED are different kinds of
// model and are only ever bound by name.
var LLMSlots = []ModelSlot{
	ModelSlotExtract, ModelSlotTimeParse, ModelSlotConsolidate, ModelSlotDedupCheck, ModelSlotReflect,
	ModelSlotStructuredOutput, ModelSlotMentalModel,
}

// EmbeddingDim is the dimension of stored embeddings (memory_units.embedding is vector(768)); the embedding model
// must return exactly this many dimensions.
const EmbeddingDim = 768

// ModelBinding binds a slot to a gateway model with its own parameters. It is atomic: the level that supplies a
// slot's binding supplies all of it, and unset fields mean the gateway / adapter default.
type ModelBinding struct {
	// Model is the gateway model name.
	Model string `json:"model,omitempty"`
	// Temperature is the sampling temperature; nil = provider default.
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP is nucleus sampling; nil = provider default.
	TopP *float64 `json:"top_p,omitempty"`
	// MaxOutputTokens caps the completion length; nil = caller decides.
	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`
	// ReasoningEffort is passed through for reasoning models; empty = unset.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// TimeoutMS bounds one call; nil = adapter default.
	TimeoutMS *int `json:"timeout_ms,omitempty"`
	// MaxRetries bounds retries on invalid output or transient errors; nil = adapter default.
	MaxRetries *int `json:"max_retries,omitempty"`
	// Extra is passed through to the OpenAI-compatible request body untouched.
	Extra map[string]any `json:"extra,omitempty"`
}

// ModelBindings binds model slots at one config level: Default for every chat-model slot (LLMSlots) the level
// leaves unset, plus one optional binding per slot. Within a level a slot's own binding wins over Default; across
// levels the most specific level that yields a binding for the slot wins (package config flattens each level with
// For before merging, so a bank's Default beats the server's per-slot binding).
type ModelBindings struct {
	// Default binds every chat-model slot this level leaves unset; never RERANK or EMBED.
	Default *ModelBinding `json:"default,omitempty"`
	// Extract binds ModelSlotExtract.
	Extract *ModelBinding `json:"extract,omitempty"`
	// TimeParse binds ModelSlotTimeParse.
	TimeParse *ModelBinding `json:"time_parse,omitempty"`
	// Consolidate binds ModelSlotConsolidate.
	Consolidate *ModelBinding `json:"consolidate,omitempty"`
	// DedupCheck binds ModelSlotDedupCheck.
	DedupCheck *ModelBinding `json:"dedup_check,omitempty"`
	// Reflect binds ModelSlotReflect.
	Reflect *ModelBinding `json:"reflect,omitempty"`
	// StructuredOutput binds ModelSlotStructuredOutput.
	StructuredOutput *ModelBinding `json:"structured_output,omitempty"`
	// MentalModel binds ModelSlotMentalModel.
	MentalModel *ModelBinding `json:"mental_model,omitempty"`
	// Rerank binds ModelSlotRerank; Default never fills it.
	Rerank *ModelBinding `json:"rerank,omitempty"`
	// Embed binds ModelSlotEmbed; only the server config file may set it, and Default never fills it.
	Embed *ModelBinding `json:"embed,omitempty"`
}

// Slot returns a pointer to the field holding slot's own binding (not Default), or nil for an unknown slot. m must
// not be nil.
func (m *ModelBindings) Slot(slot ModelSlot) **ModelBinding {
	switch slot {
	case ModelSlotExtract:
		return &m.Extract
	case ModelSlotTimeParse:
		return &m.TimeParse
	case ModelSlotConsolidate:
		return &m.Consolidate
	case ModelSlotDedupCheck:
		return &m.DedupCheck
	case ModelSlotReflect:
		return &m.Reflect
	case ModelSlotStructuredOutput:
		return &m.StructuredOutput
	case ModelSlotMentalModel:
		return &m.MentalModel
	case ModelSlotRerank:
		return &m.Rerank
	case ModelSlotEmbed:
		return &m.Embed
	default:
		return nil
	}
}

// For returns the binding this level yields for slot: the slot's own binding, else Default for a chat-model slot,
// else nil. A nil m yields nothing.
func (m *ModelBindings) For(slot ModelSlot) *ModelBinding {
	if m == nil {
		return nil
	}
	if f := m.Slot(slot); f != nil && *f != nil {
		return *f
	}
	if IsLLMSlot(slot) {
		return m.Default
	}
	return nil
}

// IsLLMSlot reports whether slot is a chat-model slot (one Default fills).
func IsLLMSlot(slot ModelSlot) bool {
	for _, s := range LLMSlots {
		if s == slot {
			return true
		}
	}
	return false
}

// Usage records one LLM (or embedding) call's token counts for per-slot cost accounting.
type Usage struct {
	// Slot is the slot the call ran under.
	Slot ModelSlot `json:"slot"`
	// Model is the gateway model that served the call.
	Model string `json:"model"`
	// InputTokens is the prompt token count reported by the gateway.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens is the completion token count reported by the gateway.
	OutputTokens int64 `json:"output_tokens"`
}
