package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/jsonschema"
	"slices"
	"sync"
)

// ErrNotScripted is returned (wrapped, with the slot and prompt name in the message) by an LLM call that
// has no scripted reply for its slot and name.
var ErrNotScripted = errors.New("fake llm: no reply scripted")

// ErrTimeout is what LLM.Timeout scripts: errors.Is matches both domain.ErrUnavailable and
// context.DeadlineExceeded, like a gateway call that ran past its deadline.
var ErrTimeout = fmt.Errorf("%w: fake llm: call timed out: %w", domain.ErrUnavailable, context.DeadlineExceeded)

// invalidJSONBody is the body InvalidJSON scripts: a truncated JSON object.
const invalidJSONBody = `{"fake llm": "this output is not valid JSON`

// Reply is one scripted LLM reply.
type Reply struct {
	// JSON is the Structured response body, json.Unmarshal-ed into the caller's out. A body that does not
	// decode makes Structured fail with an error wrapping domain.ErrUnavailable that quotes the body.
	JSON string
	// Chat is the turn Chat returns. Message.Role defaults to assistant; FinishReason defaults to
	// "tool_calls" when Message.ToolCalls is non-empty and "stop" otherwise; Usage gets the same defaults
	// as the Usage field. ToolCalls (including malformed Arguments) are returned unchanged.
	Chat ai.ChatResponse
	// Usage is the usage Structured reports; Slot defaults to the request's slot and Model to the
	// request's Binding.Model. Chat ignores it and reports Chat.Usage.
	Usage domain.Usage
	// Err, when set, makes the call return an error wrapping Err (no decoding, zero usage).
	Err error
}

// LLMCall records one LLM call. Exactly one of Structured and Chat is set.
type LLMCall struct {
	// Slot is the request's slot.
	Slot domain.ModelSlot
	// Name is the request's prompt name.
	Name string
	// Structured is a copy of the request of a Structured call; nil for a Chat call.
	Structured *ai.StructuredRequest
	// Chat is a copy of the request of a Chat call; nil for a Structured call.
	Chat *ai.ChatRequest
}

// llmKey keys a reply script.
type llmKey struct {
	slot domain.ModelSlot
	name string
}

// llmScript is the FIFO of one key; next is the index of the next reply to serve.
type llmScript struct {
	replies []Reply
	next    int
}

// LLM is a ai.LLM scripted per (slot, prompt name). Script appends replies to the key's FIFO; each call
// serves the next unserved reply, and once the FIFO is exhausted the last reply repeats forever (it is
// sticky). Replies scripted after the FIFO ran dry are served next, in order. A call uses the script of
// its exact (slot, name) if one exists, else the (slot, "") fallback script, else it fails with
// ErrNotScripted.
//
// Every call is recorded (see Calls), including failing ones. A call whose ctx is already done returns
// ctx.Err() wrapped and consumes no reply. Calls never block. It is safe for concurrent use.
type LLM struct {
	mu      sync.Mutex
	scripts map[llmKey]*llmScript
	calls   []LLMCall
}

var _ ai.LLM = (*LLM)(nil)

// NewLLM returns an LLM with no scripts.
func NewLLM() *LLM {
	return &LLM{scripts: map[llmKey]*llmScript{}}
}

// Script appends replies to the FIFO of (slot, name); name "" scripts the fallback for every prompt name
// of slot that has no script of its own.
func (l *LLM) Script(slot domain.ModelSlot, name string, replies ...Reply) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := llmKey{slot, name}
	s, ok := l.scripts[k]
	if !ok {
		s = &llmScript{}
		l.scripts[k] = s
	}
	s.replies = append(s.replies, replies...)
}

// RespondJSON scripts a Structured reply with body: Script(slot, name, Reply{JSON: body}).
func (l *LLM) RespondJSON(slot domain.ModelSlot, name, body string) {
	l.Script(slot, name, Reply{JSON: body})
}

// RespondChat scripts a Chat reply: Script(slot, name, Reply{Chat: resp}).
func (l *LLM) RespondChat(slot domain.ModelSlot, name string, resp ai.ChatResponse) {
	l.Script(slot, name, Reply{Chat: resp})
}

// Fail scripts a reply that fails with an error wrapping err.
func (l *LLM) Fail(slot domain.ModelSlot, name string, err error) {
	l.Script(slot, name, Reply{Err: err})
}

// Timeout scripts a reply that fails with ErrTimeout at once; it never blocks.
func (l *LLM) Timeout(slot domain.ModelSlot, name string) {
	l.Script(slot, name, Reply{Err: ErrTimeout})
}

// InvalidJSON scripts a Structured reply whose body is not JSON. Structured then fails with an error
// wrapping domain.ErrUnavailable that includes the raw body, as the production adapter does once it has
// retried invalid output max_retries times.
func (l *LLM) InvalidJSON(slot domain.ModelSlot, name string) {
	l.Script(slot, name, Reply{JSON: invalidJSONBody})
}

// Reset drops every script and every recorded call.
func (l *LLM) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.scripts = map[llmKey]*llmScript{}
	l.calls = nil
}

// Calls returns copies of the recorded calls in call order.
func (l *LLM) Calls() []LLMCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LLMCall, len(l.calls))
	for i, c := range l.calls {
		out[i] = copyLLMCall(c)
	}
	return out
}

// CallsFor returns copies of the recorded calls whose request had exactly this slot and name, in order.
func (l *LLM) CallsFor(slot domain.ModelSlot, name string) []LLMCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LLMCall
	for _, c := range l.calls {
		if c.Slot == slot && c.Name == name {
			out = append(out, copyLLMCall(c))
		}
	}
	return out
}

// Structured behaves like the gateway: req.Schema must be a JSON Schema (an empty or malformed one is
// domain.ErrInvalidArgument, and the call is neither recorded nor served). It then serves the next scripted
// reply of (req.Slot, req.Name), validates its JSON against req.Schema and decodes it into out (a nil out
// skips decoding). Output that is not JSON, doesn't match the schema or doesn't decode into out is
// domain.ErrUnavailable, as the gateway reports output still invalid after its retries.
func (l *LLM) Structured(ctx context.Context, req ai.StructuredRequest, out any) (domain.Usage, error) {
	sch, err := jsonschema.Compile(req.Schema)
	if err != nil {
		return domain.Usage{}, fmt.Errorf("fake llm %s/%s: %w", req.Slot, req.Name, err)
	}
	cp := copyStructuredRequest(req)
	r, err := l.serve(ctx, LLMCall{Slot: req.Slot, Name: req.Name, Structured: &cp})
	if err != nil {
		return domain.Usage{}, err
	}
	if err := decodeReply(sch, r.JSON, out); err != nil {
		return domain.Usage{}, fmt.Errorf("%w: fake llm %s/%s: invalid structured output %q: %w",
			domain.ErrUnavailable, req.Slot, req.Name, r.JSON, err)
	}
	return defaultUsage(r.Usage, req.Slot, req.Binding), nil
}

// Chat serves the next scripted reply of (req.Slot, req.Name) and returns a copy of its Chat turn.
func (l *LLM) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	cp := copyChatRequest(req)
	r, err := l.serve(ctx, LLMCall{Slot: req.Slot, Name: req.Name, Chat: &cp})
	if err != nil {
		return ai.ChatResponse{}, err
	}
	resp := r.Chat
	resp.Message = copyChatMessage(resp.Message)
	if resp.Message.Role == "" {
		resp.Message.Role = ai.ChatRoleAssistant
	}
	if resp.FinishReason == "" {
		resp.FinishReason = "stop"
		if len(resp.Message.ToolCalls) > 0 {
			resp.FinishReason = "tool_calls"
		}
	}
	resp.Usage = defaultUsage(resp.Usage, req.Slot, req.Binding)
	return resp, nil
}

// serve records c and returns the next reply for it, or the error the call fails with.
func (l *LLM) serve(ctx context.Context, c LLMCall) (Reply, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
	if err := ctx.Err(); err != nil {
		return Reply{}, fmt.Errorf("fake llm %s/%s: %w", c.Slot, c.Name, err)
	}
	s, ok := l.scripts[llmKey{c.Slot, c.Name}]
	if !ok || len(s.replies) == 0 {
		s, ok = l.scripts[llmKey{c.Slot, ""}]
	}
	if !ok || len(s.replies) == 0 {
		return Reply{}, fmt.Errorf("%w: slot %q name %q", ErrNotScripted, c.Slot, c.Name)
	}
	i := min(s.next, len(s.replies)-1)
	if s.next < len(s.replies) {
		s.next++
	}
	r := s.replies[i]
	if r.Err != nil {
		return Reply{}, fmt.Errorf("fake llm %s/%s: %w", c.Slot, c.Name, r.Err)
	}
	return r, nil
}

// decodeReply validates body against sch and decodes it into out (nil out = validate only).
func decodeReply(sch *jsonschema.Schema, body string, out any) error {
	if err := sch.Validate([]byte(body)); err != nil {
		return fmt.Errorf("fake llm: %w", err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// defaultUsage fills u's empty Slot and Model from the request.
func defaultUsage(u domain.Usage, slot domain.ModelSlot, b domain.ModelBinding) domain.Usage {
	if u.Slot == "" {
		u.Slot = slot
	}
	if u.Model == "" {
		u.Model = b.Model
	}
	return u
}

// copyLLMCall deep-copies a recorded call.
func copyLLMCall(c LLMCall) LLMCall {
	if c.Structured != nil {
		s := copyStructuredRequest(*c.Structured)
		c.Structured = &s
	}
	if c.Chat != nil {
		ch := copyChatRequest(*c.Chat)
		c.Chat = &ch
	}
	return c
}

// copyStructuredRequest copies req so later changes to the caller's Schema bytes don't leak in.
func copyStructuredRequest(req ai.StructuredRequest) ai.StructuredRequest {
	req.Schema = bytes.Clone(req.Schema)
	return req
}

// copyChatRequest copies req's messages and tools.
func copyChatRequest(req ai.ChatRequest) ai.ChatRequest {
	msgs := slices.Clone(req.Messages)
	for i, m := range msgs {
		msgs[i] = copyChatMessage(m)
	}
	req.Messages = msgs
	tools := slices.Clone(req.Tools)
	for i := range tools {
		tools[i].Parameters = bytes.Clone(tools[i].Parameters)
	}
	req.Tools = tools
	return req
}

// copyChatMessage copies m's tool calls byte for byte (malformed arguments included).
func copyChatMessage(m ai.ChatMessage) ai.ChatMessage {
	calls := slices.Clone(m.ToolCalls)
	for i := range calls {
		calls[i].Arguments = bytes.Clone(calls[i].Arguments)
	}
	m.ToolCalls = calls
	return m
}
