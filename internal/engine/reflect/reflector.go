package reflect

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/port"
	"time"
)

// Reflector answers one reflect request.
type Reflector interface {
	// Reflect runs up to max_iterations (default 10 at MID); must retrieve before done; drops citations
	// it never retrieved; stops with an error past max_total_tokens; optional structured-output pass.
	Reflect(ctx context.Context, req ReflectRequest) (ReflectResult, error)
}

// Recaller is the part of recall.Recaller the recall and search_observations tools consume;
// recall.Recaller satisfies it.
type Recaller interface {
	// Recall runs one recall request.
	Recall(ctx context.Context, req recall.RecallRequest) (recall.RecallResult, error)
}

// ReflectRequest mirrors memory.v1.ReflectRequest with domain types, plus scope fields used only by
// internal callers (the agentic mental-model refresh).
type ReflectRequest struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Bank is the bank reflected over.
	Bank port.BankRef
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
	Include ReflectInclude
	// QueryTime is the reference time for relative dates; nil = now (port.Clock).
	QueryTime *time.Time
	// Overrides are request-level config overrides; nil = none.
	Overrides *domain.BankConfig
	// Seq bounds the units every retrieval sees; zero = unbounded. Internal callers only.
	Seq port.SeqRange
	// Types limits fact types for retrieval tools; empty = all. Internal callers only.
	Types []domain.FactType
	// ExcludeMentalModels hides every mental model from the page tools. Internal callers only.
	ExcludeMentalModels bool
	// ExcludeMentalModelIDs hides specific mental models from the page tools. Internal callers only.
	ExcludeMentalModelIDs []port.MentalModelID
	// RecallInclude is what the recall tool attaches (chunks, budgets). Internal callers only.
	RecallInclude domain.RecallInclude
	// AsDocument asks for a markdown document instead of a conversational answer. Internal callers only.
	AsDocument bool
	// Context is additional context for the agent's system prompt (e.g. the mm_refresh_context an agentic
	// mental-model refresh renders); empty = none. Internal callers only.
	Context string
}

// ReflectInclude mirrors memory.v1.ReflectInclude.
type ReflectInclude struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Facts fills BasedOn; nil = true.
	Facts *bool
	// ToolCalls fills the trace's tool calls.
	ToolCalls bool
	// ToolOutputs adds each tool call's output to the trace; nil = true when ToolCalls.
	ToolOutputs *bool
}

// ReflectResult mirrors memory.v1.ReflectResponse with domain types.
type ReflectResult struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Text is the final answer (or document).
	Text string
	// StructuredOutputJSON is the projection of Text onto the response schema; nil when none or failed.
	StructuredOutputJSON json.RawMessage
	// StructuredOutputError explains a failed projection; the answer is still returned.
	StructuredOutputError string
	// BasedOn is the evidence the answer cites (only ids actually retrieved).
	BasedOn BasedOn
	// Trace explains the loop.
	Trace *ReflectTrace
	// Usage is per-slot token usage (REFLECT, STRUCTURED_OUTPUT, RERANK, ...).
	Usage []domain.Usage
}

// BasedOn mirrors memory.v1.BasedOn.
type BasedOn struct {
	// Memories are the cited memory units.
	Memories []domain.Unit
	// MentalModelIDs are the cited mental models.
	MentalModelIDs []port.MentalModelID
	// DirectiveIDs are the directives applied.
	DirectiveIDs []port.DirectiveID
}

// StopReason says why the loop ended.
type StopReason string

// Stop reasons, as reported in ReflectTrace.StopReason.
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

// ReflectTrace mirrors memory.v1.ReflectTrace.
type ReflectTrace struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// ToolCalls are the loop's tool calls (when include.tool_calls).
	ToolCalls []domain.ToolCallTrace
	// Iterations is the number of loop iterations.
	Iterations int
	// StopReason is answered | max_iterations | context_full.
	StopReason StopReason
	// DirectiveIDs are the directives rendered into the prompt.
	DirectiveIDs []port.DirectiveID
	// DurationMS is the reflect duration.
	DurationMS int64
}
