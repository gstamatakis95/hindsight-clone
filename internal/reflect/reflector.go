package reflect

import (
	"encoding/json"
	"memd/internal/domain"
	"time"
)

// Request mirrors memory.v1.Request with domain types, plus scope fields used only by
// internal callers (the agentic mental-model refresh).
type Request struct {
	// Bank is the bank reflected over.
	Bank domain.BankRef
	// Query is the question.
	Query string
	// Tags is the tag filter every retrieval tool applies; it also selects tagged directives.
	Tags domain.TagFilter
	// Budget is the effort level; unspecified = low.
	Budget domain.Budget
	// MaxTokens caps the final answer; nil = ReflectConfig.MaxAnswerTokens.
	MaxTokens *int
	// ResponseSchemaJSON asks for a structured-output projection of the answer; empty = none.
	ResponseSchemaJSON string
	// ApplyAllDirectives applies every active directive regardless of tags.
	ApplyAllDirectives bool
	// ExtraDirectives are request-only directives added to the bank's.
	ExtraDirectives []domain.Directive
	// Include selects what the result carries.
	Include Include
	// QueryTime is the reference time for relative dates; nil = now (clock.Clock).
	QueryTime *time.Time
	// Overrides are request-level config overrides; nil = none.
	Overrides *domain.BankConfig
	// Arms are the recall arms the recall and search_observations tools run; empty = the effective recall.arms.
	// An unknown or repeated name is domain.ErrInvalidArgument, before the loop starts.
	Arms []string
	// Seq bounds the units every retrieval sees; zero = unbounded. Internal callers only.
	Seq domain.SeqRange
	// Types limits fact types for retrieval tools; empty = all. Internal callers only.
	Types []domain.FactType
	// ExcludeMentalModels hides every mental model from the page tools. Internal callers only.
	ExcludeMentalModels bool
	// ExcludeMentalModelIDs hides specific mental models from the page tools. Internal callers only.
	ExcludeMentalModelIDs []domain.MentalModelID
	// RecallInclude is what the recall tool attaches (chunks, budgets). Internal callers only.
	RecallInclude domain.RecallInclude
	// AsDocument asks for a markdown document instead of a conversational answer. Internal callers only.
	AsDocument bool
	// Context is additional context for the agent's system prompt (e.g. the mm_refresh_context an agentic
	// mental-model refresh renders); empty = none. Internal callers only.
	Context string
}

// Include mirrors memory.v1.Include.
type Include struct {
	// Facts fills BasedOn; nil = true.
	Facts *bool
	// ToolCalls fills the trace's tool calls.
	ToolCalls bool
	// ToolOutputs adds each tool call's output to the trace; nil = true when ToolCalls.
	ToolOutputs *bool
}

// Result mirrors memory.v1.ReflectResponse with domain types.
type Result struct {
	// Text is the final answer (or document).
	Text string
	// StructuredOutputJSON is the projection of Text onto the response schema; nil when none or failed.
	StructuredOutputJSON json.RawMessage
	// StructuredOutputError explains a failed projection; the answer is still returned.
	StructuredOutputError string
	// BasedOn is the evidence the answer cites (only ids actually retrieved).
	BasedOn BasedOn
	// Trace explains the loop.
	Trace *Trace
	// Usage is per-slot token usage (REFLECT, STRUCTURED_OUTPUT, RERANK, ...).
	Usage []domain.Usage
}

// BasedOn mirrors memory.v1.BasedOn.
type BasedOn struct {
	// Memories are the cited memory units.
	Memories []domain.Unit
	// MentalModelIDs are the cited mental models.
	MentalModelIDs []domain.MentalModelID
	// DirectiveIDs are the directives applied.
	DirectiveIDs []domain.DirectiveID
}

// StopReason says why the loop ended.
type StopReason string

// Stop reasons, as reported in Trace.StopReason.
const (
	// StopUnspecified is the zero value.
	StopUnspecified StopReason = ""
	// StopAnswered means the agent called done.
	StopAnswered StopReason = "answered"
	// StopMaxIterations means the iteration cap forced a final answer.
	StopMaxIterations StopReason = "max_iterations"
	// StopContextFull means the context window filled and the agent synthesized from what it had.
	StopContextFull StopReason = "context_full"
)

// Trace mirrors memory.v1.Trace.
type Trace struct {
	// ToolCalls are the loop's tool calls (when include.tool_calls).
	ToolCalls []domain.ToolCallTrace
	// Iterations is the number of loop iterations.
	Iterations int
	// StopReason is answered | max_iterations | context_full.
	StopReason StopReason
	// DirectiveIDs are the directives rendered into the prompt.
	DirectiveIDs []domain.DirectiveID
	// DurationMS is the reflect duration.
	DurationMS int64
}
