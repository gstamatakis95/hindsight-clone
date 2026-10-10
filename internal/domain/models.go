package domain

// ModelSlot names one LLM-backed operation; each slot binds to its own gateway model and parameters.
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
	// ModelSlotMentalModelDelta produces edit ops for an incremental page refresh.
	ModelSlotMentalModelDelta ModelSlot = "mental_model_delta"
	// ModelSlotRerank is the cross-encoder used by recall.
	ModelSlotRerank ModelSlot = "rerank"
	// ModelSlotEmbed is usage accounting only: embedding is server-wide and never in BankConfig.
	ModelSlotEmbed ModelSlot = "embed"
	// ModelSlotSynthesize is the single-shot full mental-model refresh.
	ModelSlotSynthesize ModelSlot = "synthesize"
)

// EmbeddingDim is the dimension of the one server-wide embedding model, nomic-embed-text-v1.5.
const EmbeddingDim = 768

// ModelBinding binds a slot to a gateway model with its own parameters.
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

// SlotBinding pairs a slot with its binding; BankConfig.Models merges per slot across levels.
type SlotBinding struct {
	// Slot is the operation the binding applies to.
	Slot ModelSlot `json:"slot"`
	// Binding is the model and parameters for the slot.
	Binding ModelBinding `json:"binding"`
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
