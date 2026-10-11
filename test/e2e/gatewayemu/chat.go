package gatewayemu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// chatRequest is the subset of a chat completion body the emulator reads.
type chatRequest struct {
	// Model is the requested model (emu-<slot> under the harness).
	Model string `json:"model"`
	// Messages is the conversation.
	Messages []chatMessage `json:"messages"`
	// Tools are the offered tools.
	Tools []chatTool `json:"tools"`
	// ToolChoice is "auto", "required", "none" or a function selector.
	ToolChoice json.RawMessage `json:"tool_choice"`
	// ResponseFormat carries the json_schema of a structured call.
	ResponseFormat *struct {
		// JSONSchema is the schema and its name (the prompt template name).
		JSONSchema *struct {
			// Name is the schema name.
			Name string `json:"name"`
			// Schema is the JSON Schema of the response.
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

// chatMessage is one message of the conversation.
type chatMessage struct {
	// Role is system, user, assistant or tool.
	Role string `json:"role"`
	// Content is the text (null for a tool-calling assistant turn).
	Content *string `json:"content"`
	// ToolCalls are an assistant turn's calls.
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	// ToolCallID is the call a tool message answers.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// toolCall is a tool call in a message.
type toolCall struct {
	// ID pairs the call with its tool message.
	ID string `json:"id"`
	// Type is "function".
	Type string `json:"type"`
	// Function is the call.
	Function struct {
		// Name is the tool name.
		Name string `json:"name"`
		// Arguments is the JSON argument text.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatTool is an offered tool.
type chatTool struct {
	// Function describes the tool.
	Function struct {
		// Name is the tool name.
		Name string `json:"name"`
		// Parameters is the argument schema.
		Parameters json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// text returns the message content, "" when null.
func (m chatMessage) text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

// schemaName is the structured call's schema name, "" for other chats.
func (r *chatRequest) schemaName() string {
	if r.ResponseFormat == nil || r.ResponseFormat.JSONSchema == nil {
		return ""
	}
	return r.ResponseFormat.JSONSchema.Name
}

// op recognises the request: the schema name for a structured call, OpReflectLoop when tools are offered,
// else the operation the slot model stands for.
func (r *chatRequest) op() string {
	if name := r.schemaName(); name != "" {
		return name
	}
	if len(r.Tools) > 0 {
		return OpReflectLoop
	}
	switch modelSlot(r.Model) {
	case "reflect":
		return OpReflectFinal
	case "mental_model":
		return OpSynthesize
	default:
		return OpChat
	}
}

// lastUser returns the content of the last user message, or "".
func (r *chatRequest) lastUser() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].text()
		}
	}
	return ""
}

// firstUser returns the content of the first user message, or "".
func (r *chatRequest) firstUser() string {
	for _, m := range r.Messages {
		if m.Role == "user" {
			return m.text()
		}
	}
	return ""
}

// promptLen is the total length of every message, for usage.
func (r *chatRequest) promptLen() int {
	n := 0
	for _, m := range r.Messages {
		n += len(m.text())
	}
	return n
}

// reply is the assistant message the emulator answers with.
type reply struct {
	// content is the text (structured JSON for a structured call).
	content string
	// calls are tool calls; non-empty makes finish_reason "tool_calls".
	calls []toolCall
}

// errEmulator marks a request the emulator could not answer (a malformed prompt or an emulator bug); it is
// answered with 400 so the gateway client fails at once instead of retrying.
type errEmulator struct {
	// msg says what went wrong.
	msg string
}

// Error implements error.
func (e errEmulator) Error() string { return "gateway emulator: " + e.msg }

// emuErrorf builds an errEmulator.
func emuErrorf(format string, args ...any) error {
	return errEmulator{msg: fmt.Sprintf(format, args...)}
}

// serveChat answers /chat/completions.
func (s *Server) serveChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := readJSON(r, &req); err != nil {
		s.record(Call{Op: OpChat, Status: http.StatusBadRequest})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	op := req.op()
	call := Call{Op: op, Model: req.Model, Status: http.StatusOK}
	f := s.takeFault(op)
	call.Fault = f != nil
	if done, status := applyFault(w, r, f); done {
		call.Status = status
		s.record(call)
		return
	}
	var (
		rep reply
		err error
	)
	if f != nil && f.InvalidJSON {
		rep = reply{content: invalidJSON}
	} else {
		rep, err = answer(&req, op)
	}
	if err != nil {
		call.Status = http.StatusBadRequest
		s.record(call)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(call)
	writeJSON(w, chatResponse(&req, rep))
}

// answer builds the reply for a recognised request.
func answer(req *chatRequest, op string) (reply, error) {
	switch op {
	case OpReflectLoop:
		return reflectTurn(req)
	case OpReflectFinal, OpChat:
		return reply{content: quoteAnswer(fencedOrLines(req.lastUser()))}, nil
	case OpSynthesize:
		return reply{content: synthesizePage(req.lastUser())}, nil
	default:
		content, err := structured(req)
		return reply{content: content}, err
	}
}

// chatResponse renders an OpenAI chat completion.
func chatResponse(req *chatRequest, rep reply) map[string]any {
	msg := map[string]any{"role": "assistant", "content": rep.content}
	finish := "stop"
	if len(rep.calls) > 0 {
		msg["content"] = nil
		msg["tool_calls"] = rep.calls
		finish = "tool_calls"
	}
	out := len(rep.content)
	for _, c := range rep.calls {
		out += len(c.Function.Arguments)
	}
	return map[string]any{
		"id": "emu-chat", "object": "chat.completion", "model": req.Model,
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   newUsage(req.promptLen(), out),
	}
}

// fencedOrLines returns the quotable items of a user message: the memories and observations of its fenced
// JSON blocks, then its "- [id] text" lines.
func fencedOrLines(user string) []Item {
	mem, obs := fencedItems(user)
	return append(append(mem, obs...), sourceLines(user)...)
}

// noMemories is the answer when nothing was retrieved (never empty: memd rejects an empty answer).
const noMemories = "No relevant memories were found."

// quoteAnswer is an answer quoting every item text, one bullet each.
func quoteAnswer(items []Item) string {
	if len(items) == 0 {
		return noMemories
	}
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = "- " + it.Text
	}
	return strings.Join(lines, "\n")
}

// synthesizePage is a synthesize page: the topic as a heading and every source item quoted.
func synthesizePage(user string) string {
	topic := sectionLine(user, "Topic")
	if topic == "" {
		topic = "Summary"
	}
	return "## " + topic + "\n\n" + quoteAnswer(sourceLines(user))
}
