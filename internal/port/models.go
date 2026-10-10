package port

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
)

// ---------- models (AI gateway, OpenAI-compatible HTTP) ----------

// ModelSlot names one LLM-backed operation (alias of domain.ModelSlot).
type ModelSlot = domain.ModelSlot

// ModelBinding is a slot's model and parameters (alias of domain.ModelBinding).
type ModelBinding = domain.ModelBinding

// Usage is one call's token usage for per-slot accounting (alias of domain.Usage).
type Usage = domain.Usage

// LLM calls chat completions. The adapter holds a process-wide semaphore plus one per slot.
type LLM interface {
	// Structured makes one schema-constrained call: POST /v1/chat/completions with response_format
	// json_schema. The adapter validates against Schema, retries on invalid output (bounded by the
	// binding's max_retries) and decodes the JSON into out.
	Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error)
	// Chat makes one turn of a tool-calling loop: POST /v1/chat/completions with tools.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// StructuredRequest is one schema-constrained call.
type StructuredRequest struct {
	// Slot is for usage accounting and the per-slot semaphore.
	Slot ModelSlot
	// Binding is resolved by ModelRouter for that slot.
	Binding ModelBinding
	// Name identifies the prompt template (and its version) for tracing and scripted fakes.
	Name string
	// System is the system message.
	System string
	// User is the user message.
	User string
	// Schema is the JSON Schema of the response.
	Schema json.RawMessage
	// MaxOut caps output tokens; 0 = the binding's max_output_tokens.
	MaxOut int
}

// ChatRole is a chat message role.
type ChatRole string

// Chat roles.
const (
	// ChatRoleSystem is the system prompt.
	ChatRoleSystem ChatRole = "system"
	// ChatRoleUser is a user turn.
	ChatRoleUser ChatRole = "user"
	// ChatRoleAssistant is a model turn, possibly with tool calls.
	ChatRoleAssistant ChatRole = "assistant"
	// ChatRoleTool is a tool result answering one tool call.
	ChatRoleTool ChatRole = "tool"
)

// ChatMessage is one message of a chat conversation.
type ChatMessage struct {
	// Role is the speaker.
	Role ChatRole
	// Content is the text content.
	Content string
	// ToolCalls are the calls an assistant turn makes.
	ToolCalls []ToolCall
	// ToolCallID is the call a tool message answers.
	ToolCallID string
}

// ToolSpec declares one tool the model may call.
type ToolSpec struct {
	// Name is the tool name.
	Name string
	// Description tells the model what the tool does.
	Description string
	// Parameters is the JSON Schema of the arguments.
	Parameters json.RawMessage
}

// ToolCall is one tool invocation proposed by the model.
type ToolCall struct {
	// ID pairs the call with its tool message.
	ID string
	// Name is the tool name.
	Name string
	// Arguments is the raw JSON arguments as the model produced them (possibly malformed).
	Arguments json.RawMessage
}

// ChatRequest is one turn of a tool-calling loop.
type ChatRequest struct {
	// Slot is for usage accounting and the per-slot semaphore.
	Slot ModelSlot
	// Binding is resolved by ModelRouter for that slot.
	Binding ModelBinding
	// Name identifies the prompt template for tracing and scripted fakes.
	Name string
	// Messages is the conversation so far.
	Messages []ChatMessage
	// Tools are the callable tools; empty = plain completion.
	Tools []ToolSpec
	// ToolChoice is "auto", "none", "required" or a tool name; empty = auto.
	ToolChoice string
	// MaxOut caps output tokens; 0 = the binding's max_output_tokens.
	MaxOut int
}

// ChatResponse is the model's reply to one turn.
type ChatResponse struct {
	// Message is the assistant message (content and/or tool calls).
	Message ChatMessage
	// FinishReason is the provider's finish reason (stop, tool_calls, length, ...).
	FinishReason string
	// Usage is the turn's token usage.
	Usage Usage
}

// Embedder uses the one server-wide model, nomic-embed-text-v1.5 (768 dims, domain.EmbeddingDim).
// It adds nomic's task prefixes: "search_document: " for stored text, "search_query: " for queries.
type Embedder interface {
	// EmbedDocuments embeds stored text in batches, one vector per input, in order.
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery embeds one query.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Reranker calls the gateway's rerank route (Cohere/Jina-style body, not part of the OpenAI spec).
type Reranker interface {
	// Rerank scores each doc against query, one score per doc in order. Scores pass through when all
	// are in [0,1]; otherwise the adapter applies a sigmoid (Hindsight's rule), so raw cross-encoder
	// logits and normalized scores both work.
	Rerank(ctx context.Context, b ModelBinding, query string, docs []string) ([]float32, error)
}

// ---------- model routing & config ----------

// ModelRouter picks a slot's binding from an already-merged config.
type ModelRouter interface {
	// Resolve returns cfg's binding for slot; a slot with no model bound is domain.ErrInvalidArgument.
	Resolve(cfg domain.BankConfig, slot ModelSlot) (ModelBinding, error)
}

// ConfigResolver merges server < tenant < bank < strategy < request into the effective view.
type ConfigResolver interface {
	// Effective merges server, tenant and bank config, then each override in order.
	Effective(ctx context.Context, b BankRef, overrides ...domain.BankConfig) (domain.BankConfig, error)
	// ForScope also slots the scope's matching consolidation strategy in below the request level.
	ForScope(ctx context.Context, s ScopeKey, request *domain.BankConfig) (domain.BankConfig, error)
}
