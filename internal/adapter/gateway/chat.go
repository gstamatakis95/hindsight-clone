package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/port"
	"reflect"
	"regexp"
)

// chatPath is the OpenAI chat completions route under BaseURL.
const chatPath = "/chat/completions"

// outMessage is a chat message as sent to the gateway.
type outMessage struct {
	// Role is system, user, assistant or tool.
	Role string `json:"role"`
	// Content is the text; null for an assistant turn that only calls tools.
	Content *string `json:"content"`
	// ToolCalls are an assistant turn's tool calls.
	ToolCalls []outToolCall `json:"tool_calls,omitempty"`
	// ToolCallID is the call a tool message answers.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// outToolCall is a tool call as sent back to the gateway; arguments travel as a JSON string, so malformed
// arguments from the model round-trip unchanged.
type outToolCall struct {
	// ID pairs the call with its tool message.
	ID string `json:"id"`
	// Type is always "function".
	Type string `json:"type"`
	// Function is the called function.
	Function struct {
		// Name is the tool name.
		Name string `json:"name"`
		// Arguments is the raw argument text.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// inToolCall is a tool call as received; arguments are usually a JSON string but some gateways send an object.
type inToolCall struct {
	// ID pairs the call with its tool message.
	ID string `json:"id"`
	// Function is the called function.
	Function struct {
		// Name is the tool name.
		Name string `json:"name"`
		// Arguments is a JSON string holding the arguments, or the arguments object itself.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// outTool declares one tool to the gateway.
type outTool struct {
	// Type is always "function".
	Type string `json:"type"`
	// Function describes the tool.
	Function struct {
		// Name is the tool name.
		Name string `json:"name"`
		// Description tells the model what the tool does.
		Description string `json:"description,omitempty"`
		// Parameters is the JSON Schema of the arguments.
		Parameters json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// chatResponse is the subset of a chat completion response the client reads.
type chatResponse struct {
	// Model is the model that served the call.
	Model string `json:"model"`
	// Choices holds the completions; the client reads the first.
	Choices []struct {
		// Message is the assistant message.
		Message struct {
			// Content is the text, null when the turn only calls tools.
			Content *string `json:"content"`
			// ToolCalls are the proposed tool calls.
			ToolCalls []inToolCall `json:"tool_calls"`
		} `json:"message"`
		// FinishReason is stop, tool_calls, length, ...
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Usage is the token usage.
	Usage *apiUsage `json:"usage"`
}

// complete makes one chat completion with retries for transient errors only. It returns the first
// choice's response and the usage summed over every attempt.
func (c *Client) complete(ctx context.Context, slot domain.ModelSlot, b domain.ModelBinding, body map[string]any,
) (chatResponse, domain.Usage, error) {
	sum := domain.Usage{Slot: slot}
	var resp chatResponse
	err := c.retry(ctx, c.retriesFor(b), func() error {
		var err error
		resp, err = c.completeOnce(ctx, slot, b, body, &sum)
		return err
	})
	return resp, finalUsage(sum, b.Model), err
}

// completeOnce makes one HTTP attempt at a chat completion, records its usage and adds it to sum, even when
// the response turns out to have no choices.
func (c *Client) completeOnce(ctx context.Context, slot domain.ModelSlot, b domain.ModelBinding, body any,
	sum *domain.Usage,
) (chatResponse, error) {
	var resp chatResponse
	a := attempt{slot: slot, path: chatPath, timeout: c.timeoutFor(b)}
	if err := c.post(ctx, a, body, &resp); err != nil {
		return resp, err
	}
	u := resp.Usage.toUsage(slot, modelOf(resp.Model, b.Model))
	c.record(ctx, u)
	addUsage(sum, u)
	if len(resp.Choices) == 0 {
		return resp, transient(fmt.Errorf("%w: gateway %s: response has no choices", domain.ErrUnavailable, chatPath))
	}
	return resp, nil
}

// modelOf is the model the gateway reported, or the requested one.
func modelOf(reported, requested string) string { return orDefault(reported, requested) }

// finalUsage fills in the model of a usage sum that saw no successful attempt.
func finalUsage(u domain.Usage, model string) domain.Usage {
	u.Model = orDefault(u.Model, model)
	return u
}

// chatBody builds a chat completion body from a binding: model, sampling parameters, the output cap
// (maxOut, else the binding's max_output_tokens) and reasoning effort, then the binding's extra fields on top.
func chatBody(b domain.ModelBinding, maxOut int, msgs []outMessage) map[string]any {
	body := map[string]any{"model": b.Model, "messages": msgs}
	if b.Temperature != nil {
		body["temperature"] = *b.Temperature
	}
	if b.TopP != nil {
		body["top_p"] = *b.TopP
	}
	if maxOut == 0 && b.MaxOutputTokens != nil {
		maxOut = *b.MaxOutputTokens
	}
	if maxOut > 0 {
		body["max_tokens"] = maxOut
	}
	if b.ReasoningEffort != "" {
		body["reasoning_effort"] = b.ReasoningEffort
	}
	maps.Copy(body, b.Extra)
	return body
}

// textMessage is a plain text message.
func textMessage(role port.ChatRole, text string) outMessage {
	return outMessage{Role: string(role), Content: &text}
}

// schemaNameRE matches the characters not allowed in response_format.json_schema.name.
var schemaNameRE = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// schemaName turns a prompt template name into a valid json_schema name: [a-zA-Z0-9_-]{1,64}.
func schemaName(name string) string {
	s := schemaNameRE.ReplaceAllString(name, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return orDefault(s, "response")
}

// Structured makes one schema-constrained call (response_format json_schema), validates the output against
// req.Schema and decodes it into out. Invalid output is retried with the validation error appended to the
// conversation; invalid output, timeouts and transient gateway errors share the binding's max_retries
// budget. The returned usage sums every attempt, failed ones included.
func (c *Client) Structured(ctx context.Context, req port.StructuredRequest, out any) (port.Usage, error) {
	sum := domain.Usage{Slot: req.Slot}
	sch, err := compileSchema(req.Schema)
	if err != nil {
		return finalUsage(sum, req.Binding.Model), err
	}
	var msgs []outMessage
	if req.System != "" {
		msgs = append(msgs, textMessage(port.ChatRoleSystem, req.System))
	}
	msgs = append(msgs, textMessage(port.ChatRoleUser, req.User))
	format := map[string]any{"type": "json_schema", "json_schema": map[string]any{
		"name": schemaName(req.Name), "schema": json.RawMessage(req.Schema),
	}}
	err = c.retry(ctx, c.retriesFor(req.Binding), func() error {
		body := chatBody(req.Binding, req.MaxOut, msgs)
		body["response_format"] = format
		resp, err := c.completeOnce(ctx, req.Slot, req.Binding, body, &sum)
		if err != nil {
			return err
		}
		content := deref(resp.Choices[0].Message.Content)
		if verr := decodeValid(sch, content, out); verr != nil {
			msgs = append(msgs, textMessage(port.ChatRoleAssistant, content), textMessage(port.ChatRoleUser, verr.Error()))
			return transient(fmt.Errorf("%w: gateway %s: %w", domain.ErrUnavailable, chatPath, verr))
		}
		return nil
	})
	return finalUsage(sum, req.Binding.Model), err
}

// decodeValid validates content against sch and, when it matches, decodes it into out (a nil out skips
// decoding).
func decodeValid(sch *schema, content string, out any) error {
	if err := sch.validate([]byte(content)); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// Decode into a scratch value first, so a failed attempt never leaves partial fields in out.
	if t := reflect.TypeOf(out); t.Kind() == reflect.Pointer && t.Elem().Kind() != reflect.Interface {
		if err := json.Unmarshal([]byte(content), reflect.New(t.Elem()).Interface()); err != nil {
			return fmt.Errorf("response does not decode into the expected type: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("response does not decode into the expected type: %w", err)
	}
	return nil
}

// deref returns *s, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Chat makes one turn of a tool-calling loop: POST /chat/completions with the conversation and tools.
// Transient gateway errors and timeouts retry within the binding's max_retries; the returned usage sums
// every attempt.
func (c *Client) Chat(ctx context.Context, req port.ChatRequest) (port.ChatResponse, error) {
	body := chatBody(req.Binding, req.MaxOut, toOutMessages(req.Messages))
	if len(req.Tools) > 0 {
		body["tools"] = toOutTools(req.Tools)
		if choice := toolChoice(req.ToolChoice); choice != nil {
			body["tool_choice"] = choice
		}
	}
	resp, usage, err := c.complete(ctx, req.Slot, req.Binding, body)
	if err != nil {
		return port.ChatResponse{Usage: usage}, err
	}
	choice := resp.Choices[0]
	msg := port.ChatMessage{Role: port.ChatRoleAssistant, Content: deref(choice.Message.Content)}
	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, port.ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Arguments: rawArguments(tc.Function.Arguments),
		})
	}
	return port.ChatResponse{Message: msg, FinishReason: choice.FinishReason, Usage: usage}, nil
}

// rawArguments unwraps tool-call arguments sent as a JSON string; anything else is kept as sent.
func rawArguments(raw json.RawMessage) json.RawMessage {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return json.RawMessage(s)
	}
	return raw
}

// toOutMessages converts the conversation to wire messages. Tool-call arguments are sent as strings, and an
// assistant turn with tool calls and no text sends null content.
func toOutMessages(in []port.ChatMessage) []outMessage {
	out := make([]outMessage, 0, len(in))
	for _, m := range in {
		om := textMessage(m.Role, m.Content)
		om.ToolCallID = m.ToolCallID
		for _, tc := range m.ToolCalls {
			var otc outToolCall
			otc.ID, otc.Type = tc.ID, "function"
			otc.Function.Name, otc.Function.Arguments = tc.Name, string(tc.Arguments)
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		if len(om.ToolCalls) > 0 && m.Content == "" {
			om.Content = nil
		}
		out = append(out, om)
	}
	return out
}

// toOutTools converts tool specs to wire tools.
func toOutTools(in []port.ToolSpec) []outTool {
	out := make([]outTool, len(in))
	for i, t := range in {
		out[i].Type = "function"
		out[i].Function.Name, out[i].Function.Description, out[i].Function.Parameters = t.Name, t.Description, t.Parameters
	}
	return out
}

// toolChoice maps ToolChoice to the wire value: nil for empty (provider default, auto), the keyword for
// auto/none/required, and a function selector for a tool name.
func toolChoice(choice string) any {
	switch choice {
	case "":
		return nil
	case "auto", "none", "required":
		return choice
	default:
		return map[string]any{"type": "function", "function": map[string]any{"name": choice}}
	}
}
