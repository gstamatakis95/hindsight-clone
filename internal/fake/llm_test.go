package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"strings"
	"testing"
)

// llmAnswer is the decoded shape of the scripted Structured bodies in these tests.
type llmAnswer struct {
	N int `json:"n"`
}

// structuredN makes a Structured call for (slot, name) and returns the decoded n.
func structuredN(t *testing.T, l *fake.LLM, slot port.ModelSlot, name string) (int, error) {
	t.Helper()
	var out llmAnswer
	if _, err := l.Structured(t.Context(), port.StructuredRequest{Slot: slot, Name: name}, &out); err != nil {
		return 0, fmt.Errorf("structured %s/%s: %w", slot, name, err)
	}
	return out.N, nil
}

func TestLLMFIFOAndStickyLast(t *testing.T) {
	l := fake.NewLLM()
	l.Script(domain.ModelSlotExtract, "extract_facts",
		fake.Reply{JSON: `{"n":1}`}, fake.Reply{JSON: `{"n":2}`})
	for i, want := range []int{1, 2, 2, 2} {
		got, err := structuredN(t, l, domain.ModelSlotExtract, "extract_facts")
		if err != nil || got != want {
			t.Errorf("call %d = %d, %v; want %d", i, got, err, want)
		}
	}
	l.RespondJSON(domain.ModelSlotExtract, "extract_facts", `{"n":3}`)
	for i, want := range []int{3, 3} {
		if got, err := structuredN(t, l, domain.ModelSlotExtract, "extract_facts"); err != nil || got != want {
			t.Errorf("after re-script, call %d = %d, %v; want %d", i, got, err, want)
		}
	}
}

func TestLLMNameFallback(t *testing.T) {
	l := fake.NewLLM()
	l.RespondJSON(domain.ModelSlotExtract, "", `{"n":7}`)
	l.RespondJSON(domain.ModelSlotExtract, "exact", `{"n":8}`)
	cases := []struct {
		slot    port.ModelSlot
		name    string
		want    int
		wantErr error
	}{
		{domain.ModelSlotExtract, "exact", 8, nil},
		{domain.ModelSlotExtract, "anything_else", 7, nil},
		{domain.ModelSlotExtract, "", 7, nil},
		{domain.ModelSlotConsolidate, "exact", 0, fake.ErrNotScripted},
	}
	for _, c := range cases {
		got, err := structuredN(t, l, c.slot, c.name)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s/%s = %d, %v; want %d, %v", c.slot, c.name, got, err, c.want, c.wantErr)
		}
	}
}

func TestLLMNotScripted(t *testing.T) {
	l := fake.NewLLM()
	_, err := structuredN(t, l, domain.ModelSlotReflect, "reflect_answer")
	if !errors.Is(err, fake.ErrNotScripted) {
		t.Fatalf("error = %v, want ErrNotScripted", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "reflect_answer") || !strings.Contains(msg, "reflect") {
		t.Errorf("error %q does not name the slot and prompt", msg)
	}
	_, err = l.Chat(t.Context(), port.ChatRequest{Slot: domain.ModelSlotReflect})
	if !errors.Is(err, fake.ErrNotScripted) {
		t.Errorf("Chat error = %v, want ErrNotScripted", err)
	}
}

func TestLLMErrors(t *testing.T) {
	boom := errors.New("gateway 502")
	cases := []struct {
		name   string
		script func(l *fake.LLM)
		want   []error
	}{
		{
			"timeout", func(l *fake.LLM) { l.Timeout(domain.ModelSlotExtract, "p") },
			[]error{context.DeadlineExceeded, domain.ErrUnavailable, fake.ErrTimeout},
		},
		{
			"invalid json", func(l *fake.LLM) { l.InvalidJSON(domain.ModelSlotExtract, "p") },
			[]error{domain.ErrUnavailable},
		},
		{"fail", func(l *fake.LLM) { l.Fail(domain.ModelSlotExtract, "p", boom) }, []error{boom}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := fake.NewLLM()
			c.script(l)
			var out llmAnswer
			u, err := l.Structured(t.Context(), port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: "p"}, &out)
			for _, w := range c.want {
				if !errors.Is(err, w) {
					t.Errorf("error = %v, want errors.Is %v", err, w)
				}
			}
			if u != (port.Usage{}) {
				t.Errorf("usage on error = %+v, want zero", u)
			}
		})
	}
}

func TestLLMInvalidJSONIncludesBody(t *testing.T) {
	l := fake.NewLLM()
	l.RespondJSON(domain.ModelSlotExtract, "p", `{"n": oops}`)
	var out llmAnswer
	_, err := l.Structured(t.Context(), port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: "p"}, &out)
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "oops") {
		t.Errorf("error = %v, want ErrUnavailable quoting the body", err)
	}
}

func TestLLMChatMalformedToolCall(t *testing.T) {
	l := fake.NewLLM()
	bad := json.RawMessage(`{"query": "unterminated`)
	l.RespondChat(domain.ModelSlotReflect, "reflect_loop", port.ChatResponse{
		Message: port.ChatMessage{ToolCalls: []port.ToolCall{{ID: "c1", Name: "recall", Arguments: bad}}},
	})
	resp, err := l.Chat(t.Context(), port.ChatRequest{
		Slot: domain.ModelSlotReflect, Name: "reflect_loop", Binding: port.ModelBinding{Model: "m1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Role != port.ChatRoleAssistant || resp.FinishReason != "tool_calls" {
		t.Errorf("role %q finish %q, want assistant, tool_calls", resp.Message.Role, resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 || string(resp.Message.ToolCalls[0].Arguments) != string(bad) {
		t.Errorf("tool calls = %+v, want the malformed arguments unchanged", resp.Message.ToolCalls)
	}
	if want := (port.Usage{Slot: domain.ModelSlotReflect, Model: "m1"}); resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
	resp.Message.ToolCalls[0].Arguments[0] = 'X'
	again, _ := l.Chat(t.Context(), port.ChatRequest{Slot: domain.ModelSlotReflect, Name: "reflect_loop"})
	if string(again.Message.ToolCalls[0].Arguments) != string(bad) {
		t.Error("mutating a returned response changed the script")
	}
}

func TestLLMUsageDefaults(t *testing.T) {
	l := fake.NewLLM()
	l.Script(domain.ModelSlotExtract, "a", fake.Reply{JSON: `{}`, Usage: port.Usage{InputTokens: 10, OutputTokens: 2}})
	l.Script(domain.ModelSlotExtract, "b",
		fake.Reply{JSON: `{}`, Usage: port.Usage{Slot: "other", Model: "explicit", InputTokens: 1}})
	b := port.ModelBinding{Model: "gpt-x"}
	cases := []struct {
		name string
		want port.Usage
	}{
		{"a", port.Usage{Slot: domain.ModelSlotExtract, Model: "gpt-x", InputTokens: 10, OutputTokens: 2}},
		{"b", port.Usage{Slot: "other", Model: "explicit", InputTokens: 1}},
	}
	for _, c := range cases {
		var out map[string]any
		req := port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: c.name, Binding: b}
		u, err := l.Structured(t.Context(), req, &out)
		if err != nil || u != c.want {
			t.Errorf("%s: usage %+v, %v; want %+v", c.name, u, err, c.want)
		}
	}
}

func TestLLMCallsRecorded(t *testing.T) {
	l := fake.NewLLM()
	l.RespondJSON(domain.ModelSlotExtract, "x", `{"n":1}`)
	l.RespondChat(domain.ModelSlotReflect, "loop", port.ChatResponse{Message: port.ChatMessage{Content: "hi"}})
	schema := json.RawMessage(`{"type":"object"}`)
	var out llmAnswer
	_, _ = l.Structured(t.Context(), port.StructuredRequest{
		Slot: domain.ModelSlotExtract, Name: "x", User: "u1", Schema: schema,
	}, &out)
	_, _ = l.Chat(t.Context(), port.ChatRequest{
		Slot: domain.ModelSlotReflect, Name: "loop",
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "q"}},
	})
	_, _ = structuredN(t, l, domain.ModelSlotExtract, "unscripted")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := l.Structured(ctx, port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: "x"}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Structured error = %v, want context.Canceled", err)
	}
	calls := l.Calls()
	if len(calls) != 4 {
		t.Fatalf("Calls() = %d calls, want 4", len(calls))
	}
	if c := calls[0]; c.Structured == nil || c.Chat != nil || c.Structured.User != "u1" ||
		string(c.Structured.Schema) != string(schema) {
		t.Errorf("call 0 = %+v", c)
	}
	if c := calls[1]; c.Chat == nil || c.Structured != nil || c.Chat.Messages[0].Content != "q" {
		t.Errorf("call 1 = %+v", c)
	}
	if got := l.CallsFor(domain.ModelSlotExtract, "x"); len(got) != 2 {
		t.Errorf("CallsFor(extract, x) = %d calls, want 2", len(got))
	}
	if got := l.CallsFor(domain.ModelSlotExtract, "unscripted"); len(got) != 1 {
		t.Errorf("CallsFor(extract, unscripted) = %d calls, want 1", len(got))
	}
	calls[0].Structured.Schema[0] = 'X'
	if string(l.Calls()[0].Structured.Schema) != string(schema) {
		t.Error("Calls() shares request bytes")
	}
	l.Reset()
	if len(l.Calls()) != 0 {
		t.Error("Reset kept calls")
	}
	if _, err := structuredN(t, l, domain.ModelSlotExtract, "x"); !errors.Is(err, fake.ErrNotScripted) {
		t.Errorf("after Reset error = %v, want ErrNotScripted", err)
	}
}

func TestLLMCancelledDoesNotConsume(t *testing.T) {
	l := fake.NewLLM()
	l.Script(domain.ModelSlotExtract, "x", fake.Reply{JSON: `{"n":1}`}, fake.Reply{JSON: `{"n":2}`})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out llmAnswer
	if _, err := l.Structured(ctx, port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: "x"}, &out); err == nil {
		t.Fatal("cancelled call succeeded")
	}
	if got, err := structuredN(t, l, domain.ModelSlotExtract, "x"); err != nil || got != 1 {
		t.Errorf("first live call = %d, %v; want 1", got, err)
	}
}
