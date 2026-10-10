package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/adapter/gateway"
	"memd/internal/domain"
	"memd/internal/port"
	"net/http"
	"strings"
	"testing"
	"time"
)

// answerSchema requires {"answer": string}.
const answerSchema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],` +
	`"additionalProperties":false}`

// answer is the decoded form of answerSchema.
type answer struct {
	Answer string `json:"answer"`
}

// structuredReq is a structured request for answerSchema on the extract slot.
func structuredReq(b domain.ModelBinding) port.StructuredRequest {
	return port.StructuredRequest{
		Slot: domain.ModelSlotExtract, Binding: b, Name: "retain.extract/v1@abc", System: "sys", User: "usr",
		Schema: json.RawMessage(answerSchema),
	}
}

func TestStructuredRetriesWithValidationError(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(n int, _ map[string]any) (int, any) {
		if n == 0 {
			return http.StatusOK, chatReply(`{"answer": 5}`, 10, 3)
		}
		return http.StatusOK, chatReply(`{"answer": "ok"}`, 20, 4)
	}
	b := binding("m")
	b.MaxRetries = ptr(1)
	var out answer
	u, err := g.client(gateway.Config{}).Structured(t.Context(), structuredReq(b), &out)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if out.Answer != "ok" {
		t.Errorf("out = %+v, want answer ok", out)
	}
	want := domain.Usage{Slot: domain.ModelSlotExtract, Model: "served-model", InputTokens: 30, OutputTokens: 7}
	if u != want {
		t.Errorf("usage = %+v, want %+v (summed over both attempts)", u, want)
	}
	reqs := g.bodies("chat")
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	msgs, _ := reqs[1]["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("retry has %d messages, want 4 (system, user, bad answer, error): %s", len(msgs), mustJSON(t, msgs))
	}
	bad, _ := msgs[2].(map[string]any)
	if bad["role"] != "assistant" || bad["content"] != `{"answer": 5}` {
		t.Errorf("message 2 = %v, want the invalid assistant output", bad)
	}
	fix, _ := msgs[3].(map[string]any)
	if content := str(fix["content"]); fix["role"] != "user" || !strings.Contains(content, "$.answer") ||
		!strings.Contains(content, "want type string") {
		t.Errorf("message 3 = %v, want a user message carrying the validation error", fix)
	}
}

func TestStructuredGivesUpAfterMaxRetries(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(int, map[string]any) (int, any) { return http.StatusOK, chatReply(`not json`, 1, 1) }
	b := binding("m")
	b.MaxRetries = ptr(2)
	var out answer
	u, err := g.client(gateway.Config{}).Structured(t.Context(), structuredReq(b), &out)
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if n := len(g.bodies("chat")); n != 3 {
		t.Errorf("got %d requests, want 3 (1 + max_retries 2)", n)
	}
	if u.InputTokens != 3 || u.OutputTokens != 3 {
		t.Errorf("usage = %+v, want 3/3 summed over failed attempts", u)
	}
}

func TestStructuredRequestBody(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(int, map[string]any) (int, any) { return http.StatusOK, chatReply(`{"answer":"x"}`, 1, 1) }
	b := domain.ModelBinding{
		Model: "m", Temperature: ptr(0.2), TopP: ptr(0.9), MaxOutputTokens: ptr(500), ReasoningEffort: "low",
		Extra: map[string]any{"seed": 7},
	}
	req := structuredReq(b)
	req.MaxOut = 100
	if _, err := g.client(gateway.Config{}).Structured(t.Context(), req, nil); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	body := g.bodies("chat")[0]
	checks := map[string]any{
		"model": "m", "temperature": 0.2, "top_p": 0.9, "max_tokens": 100.0, "reasoning_effort": "low", "seed": 7.0,
	}
	for k, want := range checks {
		if body[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, body[k], want)
		}
	}
	rf, _ := body["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["name"] != "retain_extract_v1_abc" || js["strict"] != nil {
		t.Errorf("response_format = %v, want json_schema named retain_extract_v1_abc, strict unset", rf)
	}
	var wantSchema any
	_ = json.Unmarshal([]byte(answerSchema), &wantSchema)
	if got, want := mustJSON(t, js["schema"]), mustJSON(t, wantSchema); got != want {
		t.Errorf("schema = %s, want %s", got, want)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 || mustJSON(t, msgs[0]) != `{"content":"sys","role":"system"}` {
		t.Errorf("messages = %s, want system then user", mustJSON(t, msgs))
	}
}

func TestChatToolCalls(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(int, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{
			"model": "served-model",
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
					map[string]any{"id": "c1", "type": "function", "function": map[string]any{
						"name": "search", "arguments": `{"q":"x"}`,
					}},
				}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5},
		}
	}
	params := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	for _, tc := range []struct {
		choice string
		want   string
	}{
		{"", "null"},
		{"required", `"required"`},
		{"search", `{"function":{"name":"search"},"type":"function"}`},
	} {
		req := port.ChatRequest{
			Slot: domain.ModelSlotReflect, Binding: binding("m"), Name: "reflect",
			Messages:   []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
			Tools:      []port.ToolSpec{{Name: "search", Description: "find", Parameters: params}, {Name: "done"}},
			ToolChoice: tc.choice,
		}
		resp, err := g.client(gateway.Config{}).Chat(t.Context(), req)
		if err != nil {
			t.Fatalf("Chat: %v", err)
		}
		body := g.bodies("chat")[len(g.bodies("chat"))-1]
		if got := mustJSON(t, body["tool_choice"]); got != tc.want {
			t.Errorf("choice %q: tool_choice = %s, want %s", tc.choice, got, tc.want)
		}
		tools := mustJSON(t, body["tools"])
		wantTools := `[{"function":{"description":"find","name":"search","parameters":{"properties":{"q":{"type":` +
			`"string"}},"type":"object"}},"type":"function"},{"function":{"name":"done"},"type":"function"}]`
		if tools != wantTools {
			t.Errorf("tools = %s, want %s", tools, wantTools)
		}
		want := port.ToolCall{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)}
		if len(resp.Message.ToolCalls) != 1 || mustJSON(t, resp.Message.ToolCalls[0]) != mustJSON(t, want) {
			t.Errorf("tool calls = %+v, want [%+v]", resp.Message.ToolCalls, want)
		}
		if resp.FinishReason != "tool_calls" || resp.Message.Role != port.ChatRoleAssistant {
			t.Errorf("response = %+v", resp)
		}
		if resp.Usage != (domain.Usage{
			Slot: domain.ModelSlotReflect, Model: "served-model", InputTokens: 11,
			OutputTokens: 5,
		}) {
			t.Errorf("usage = %+v", resp.Usage)
		}
	}
}

func TestChatMalformedToolArgumentsRoundTrip(t *testing.T) {
	const malformed = `{"q": "unterminated`
	g := newFakeGateway(t)
	g.chat = func(n int, _ map[string]any) (int, any) {
		if n > 0 {
			return http.StatusOK, chatReply("done", 1, 1)
		}
		return http.StatusOK, map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "c1", "type": "function", "function": map[string]any{"name": "search", "arguments": malformed},
			}}},
			"finish_reason": "tool_calls",
		}}}
	}
	c := g.client(gateway.Config{})
	req := port.ChatRequest{
		Slot: domain.ModelSlotReflect, Binding: binding("m"),
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
		Tools:    []port.ToolSpec{{Name: "search"}},
	}
	resp, err := c.Chat(t.Context(), req)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if got := string(resp.Message.ToolCalls[0].Arguments); got != malformed {
		t.Fatalf("arguments = %q, want the malformed text unchanged", got)
	}
	req.Messages = append(req.Messages, resp.Message,
		port.ChatMessage{Role: port.ChatRoleTool, ToolCallID: "c1", Content: "error: arguments are not valid JSON"})
	if _, err := c.Chat(t.Context(), req); err != nil {
		t.Fatalf("turn 2 with malformed arguments in history: %v", err)
	}
	msgs, _ := g.bodies("chat")[1]["messages"].([]any)
	asst, _ := msgs[1].(map[string]any)
	calls, _ := asst["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	fn, _ := call["function"].(map[string]any)
	if fn["arguments"] != malformed || asst["content"] != nil || call["type"] != "function" {
		t.Errorf("assistant message sent back = %s", mustJSON(t, asst))
	}
	tool, _ := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "c1" {
		t.Errorf("tool message = %s", mustJSON(t, tool))
	}
}

func TestTimeoutHonoursBindingTimeoutMS(t *testing.T) {
	g := newFakeGateway(t)
	g.delay = 5 * time.Second
	g.chat = func(int, map[string]any) (int, any) { return http.StatusOK, chatReply("late", 1, 1) }
	c := g.client(gateway.Config{})
	b := binding("m")
	b.TimeoutMS, b.MaxRetries = ptr(50), ptr(1)
	start := time.Now()
	_, err := c.Chat(t.Context(), port.ChatRequest{Slot: domain.ModelSlotReflect, Binding: b})
	if !errors.Is(err, domain.ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("took %v, want about 2 x 50ms", el)
	}
	if n := len(g.bodies("chat")); n != 2 {
		t.Errorf("got %d attempts, want 2 (timeout_ms is per attempt, retried once)", n)
	}

	// The caller's own deadline is not retried and is not reported as a gateway failure.
	b.TimeoutMS, b.MaxRetries = ptr(10_000), ptr(3)
	_, err = c.Chat(withTimeout(t, 50*time.Millisecond), port.ChatRequest{Slot: domain.ModelSlotReflect, Binding: b})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("caller deadline: err = %v, want DeadlineExceeded only", err)
	}
	if n := len(g.bodies("chat")); n != 3 {
		t.Errorf("caller deadline: got %d total attempts, want 3 (no retry)", n)
	}
}

func TestTransientErrorsRetry(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(n int, _ map[string]any) (int, any) {
		switch n {
		case 0:
			return http.StatusServiceUnavailable, map[string]any{"error": "busy"}
		case 1:
			return http.StatusTooManyRequests, map[string]any{"error": "slow down"}
		default:
			return http.StatusOK, chatReply("fine", 1, 1)
		}
	}
	b := binding("m")
	b.MaxRetries = ptr(2)
	resp, err := g.client(gateway.Config{}).Chat(t.Context(), port.ChatRequest{Slot: domain.ModelSlotReflect, Binding: b})
	if err != nil || resp.Message.Content != "fine" {
		t.Fatalf("Chat = %+v, %v; want fine after two retries", resp, err)
	}
}

func TestClientErrorNotRetried(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(int, map[string]any) (int, any) {
		return http.StatusBadRequest, map[string]any{"error": "bad request"}
	}
	b := binding("m")
	b.MaxRetries = ptr(3)
	_, err := g.client(gateway.Config{}).Chat(t.Context(), port.ChatRequest{Slot: domain.ModelSlotReflect, Binding: b})
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "400") {
		t.Errorf("err = %v, want ErrUnavailable with status 400", err)
	}
	if n := len(g.bodies("chat")); n != 1 {
		t.Errorf("got %d attempts, want 1", n)
	}
}

func TestStructuredRejectsMissingSchema(t *testing.T) {
	g := newFakeGateway(t)
	req := structuredReq(binding("m"))
	req.Schema = nil
	if _, err := g.client(gateway.Config{}).Structured(t.Context(), req, nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("err = %v, want ErrInvalidArgument", err)
	}
	if n := len(g.bodies("chat")); n != 0 {
		t.Errorf("got %d requests, want none", n)
	}
}

func TestStructuredFailedDecodeLeavesNoPartialFields(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(n int, _ map[string]any) (int, any) {
		if n == 0 { // valid per schema, but 1.5 does not decode into an int
			return http.StatusOK, chatReply(`{"answer":"stale","n":1.5}`, 1, 1)
		}
		return http.StatusOK, chatReply(`{"answer":"ok"}`, 1, 1)
	}
	b := binding("m")
	b.MaxRetries = ptr(1)
	req := structuredReq(b)
	req.Schema = json.RawMessage(`{"type":"object","required":["answer"],` +
		`"properties":{"answer":{"type":"string"},"n":{"type":"number"}}}`)
	var out struct {
		Answer string `json:"answer"`
		N      int    `json:"n"`
	}
	if _, err := g.client(gateway.Config{}).Structured(t.Context(), req, &out); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if out.Answer != "ok" || out.N != 0 {
		t.Errorf("out = %+v, want only the accepted answer's fields", out)
	}
	if n := len(g.bodies("chat")); n != 2 {
		t.Errorf("got %d requests, want 2", n)
	}
}
