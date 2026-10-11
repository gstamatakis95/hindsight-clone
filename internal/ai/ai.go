package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"

	"github.com/tiktoken-go/tokenizer"
)

// LLM calls chat completions. The adapter holds a process-wide semaphore plus one per slot.
type LLM interface {
	// Structured makes one schema-constrained call: POST /v1/chat/completions with response_format
	// json_schema. The adapter validates against Schema, retries on invalid output (bounded by the
	// binding's max_retries) and decodes the JSON into out.
	Structured(ctx context.Context, req StructuredRequest, out any) (domain.Usage, error)
	// Chat makes one turn of a tool-calling loop: POST /v1/chat/completions with tools.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// StructuredRequest is one schema-constrained call.
type StructuredRequest struct {
	// Slot is for usage accounting and the per-slot semaphore.
	Slot domain.ModelSlot
	// Binding is resolved by Router for that slot.
	Binding domain.ModelBinding
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
	Slot domain.ModelSlot
	// Binding is resolved by Router for that slot.
	Binding domain.ModelBinding
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
	Usage domain.Usage
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
	Rerank(ctx context.Context, b domain.ModelBinding, query string, docs []string) ([]float32, error)
}

// Router picks a slot's model binding from an already-merged config. It holds no state, so the zero value and
// one Router shared by every caller are both fine.
type Router struct{}

// NewRouter returns the model router.
func NewRouter() *Router { return &Router{} }

// Resolve returns the binding cfg yields for slot: the slot's own binding, else models.default for a chat-model
// slot (domain.ModelBindings.For). cfg is normally the effective config, whose levels package config has already
// merged so that the most specific level yielding a binding won. EMBED (the server-wide embedder takes no binding)
// and the unspecified slot are domain.ErrInvalidArgument. A slot with no model is domain.ErrModelUnbound
// (FAILED_PRECONDITION: the request is fine, the server, tenant or bank config lacks a binding).
func (*Router) Resolve(cfg domain.BankConfig, slot domain.ModelSlot) (domain.ModelBinding, error) {
	if slot == domain.ModelSlotUnspecified || slot == domain.ModelSlotEmbed {
		return domain.ModelBinding{}, fmt.Errorf("%w: slot %q has no per-call binding", domain.ErrInvalidArgument, slot)
	}
	b, ok := cfg.Binding(slot)
	if !ok {
		hint := "models.default or models." + string(slot)
		if !domain.IsLLMSlot(slot) {
			hint = "models." + string(slot)
		}
		return domain.ModelBinding{}, fmt.Errorf("%w: no model bound to slot %q (set %s in the server config file, "+
			"or in the tenant or bank config)", domain.ErrModelUnbound, slot, hint)
	}
	return b, nil
}

// Tokenizer counts tokens with tiktoken's cl100k_base encoding, embedded in the binary (no download at start). One
// value serves recall's packing and include budgets, retain's chunker and consolidation's source-fact budgets. The
// codec only reads its tables after construction, so it is safe for concurrent use.
type Tokenizer struct{ codec tokenizer.Codec }

// NewTokenizer loads the cl100k_base encoding.
func NewTokenizer() (*Tokenizer, error) {
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: %w", err)
	}
	return &Tokenizer{codec: codec}, nil
}

// Count returns the number of tokens in text. A text the codec can't encode (it never happens for valid UTF-8) falls
// back to about four characters per token, so budgets never break on one odd input.
func (t *Tokenizer) Count(text string) int {
	n, err := t.codec.Count(text)
	if err != nil {
		return (len(text) + 3) / 4
	}
	return n
}
