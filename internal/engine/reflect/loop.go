package reflect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"memd/internal/prompts"
	"strings"
)

// errMustSearch is the tool error a done call gets before any retrieval (Hindsight's wording).
const errMustSearch = "You must search for information first. Use search_mental_models(), search_observations(), " +
	"or recall() before providing your final answer."

// overflowMarkers are the provider error texts that mean the prompt overflowed the context window
// (Hindsight's _is_context_overflow list, minus the ambiguous resource_exhausted; decision 1101).
var overflowMarkers = []string{
	"context_length_exceeded", "context length exceeded", "maximum context length", "prompt_too_long",
	"prompt is too long", "input is too long", "too many tokens", "context window",
}

// isContextOverflow reports whether a chat error is a context-window overflow.
func isContextOverflow(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, m := range overflowMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// answer is the loop's outcome.
type answer struct {
	text    string
	cited   []string
	viaDone bool
	stop    StopReason
}

// run is one reflect loop.
type run struct {
	llm         port.LLM
	clock       port.Clock
	binding     port.ModelBinding
	st          *AgentState
	tools       map[string]Tool
	specs       []port.ToolSpec
	data        prompts.ReflectData
	maxIter     int
	maxTotal    *int64
	requireEv   bool
	messages    []port.ChatMessage
	outputs     []prompts.ToolOutput
	sawToolCall bool
	callIDs     map[string]bool
}

// loop runs up to maxIter tool-calling turns, then a closing done turn and, failing that, a tool-less
// final synthesis.
func (r *run) loop(ctx context.Context) (answer, error) {
	for i := 1; i <= r.maxIter; i++ {
		r.st.Iteration = i
		resp, err := r.chat(ctx, prompts.ReflectSystem, r.specs, "auto")
		if err != nil {
			if isContextOverflow(err) {
				return r.synthesize(ctx, StopContextFull)
			}
			return answer{}, err
		}
		if len(resp.Message.ToolCalls) == 0 {
			if !r.sawToolCall {
				return answer{}, fmt.Errorf("%w: reflect: the model answered without calling a tool (finish %q); "+
					"the REFLECT model must support tool calls", domain.ErrUnavailable, resp.FinishReason)
			}
			return r.finish(ctx, StopAnswered)
		}
		r.sawToolCall = true
		ans, done, err := r.handle(ctx, resp.Message.ToolCalls)
		if err != nil || done {
			return ans, err
		}
	}
	return r.finish(ctx, StopMaxIterations)
}

// chat makes one REFLECT chat call over the conversation and charges its usage.
func (r *run) chat(ctx context.Context, name string, tools []port.ToolSpec, choice string) (port.ChatResponse, error) {
	resp, err := r.llm.Chat(ctx, port.ChatRequest{
		Slot: domain.ModelSlotReflect, Binding: r.binding, Name: name, Messages: r.messages, Tools: tools,
		ToolChoice: choice,
	})
	if uerr := addUsage(r.st, resp.Usage, r.maxTotal); uerr != nil {
		return port.ChatResponse{}, uerr
	}
	if err != nil {
		return port.ChatResponse{}, fmt.Errorf("reflect: chat: %w", err)
	}
	return resp, nil
}

// normalizeName strips the prefixes some providers put on tool names.
func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "functions.")
	if i := strings.Index(name, "<|"); i >= 0 {
		name = name[:i]
	}
	return name
}

// uniqueCalls gives every call a non-empty id unique within the run (gateways may send blank or repeated ids).
func (r *run) uniqueCalls(calls []port.ToolCall) []port.ToolCall {
	out := make([]port.ToolCall, len(calls))
	for i, c := range calls {
		c.Name = normalizeName(c.Name)
		if c.ID == "" || r.callIDs[c.ID] {
			c.ID = fmt.Sprintf("call_%d_%d", r.st.Iteration, i)
		}
		r.callIDs[c.ID] = true
		out[i] = c
	}
	return out
}

// handle runs one turn's tool calls. A done call wins: the turn's other calls are dropped.
func (r *run) handle(ctx context.Context, calls []port.ToolCall) (answer, bool, error) {
	calls = r.uniqueCalls(calls)
	for _, c := range calls {
		if c.Name == ToolDone {
			return r.handleDone(c)
		}
	}
	r.messages = append(r.messages, port.ChatMessage{Role: port.ChatRoleAssistant, ToolCalls: calls})
	for _, c := range calls {
		if err := r.callTool(ctx, c); err != nil {
			return answer{}, false, err
		}
	}
	return answer{}, false, nil
}

// handleDone accepts a done call, or sends it back: before any retrieval (require_evidence) and when its
// arguments are malformed. A present but blank answer fails reflect.
func (r *run) handleDone(c port.ToolCall) (answer, bool, error) {
	r.messages = append(r.messages, port.ChatMessage{Role: port.ChatRoleAssistant, ToolCalls: []port.ToolCall{c}})
	if r.requireEv && !r.st.Searched {
		r.toolMessage(c.ID, malformed("%s", errMustSearch).Output)
		return answer{}, false, nil
	}
	text, cited, problem := parseDone(c.Arguments, r.data.AnswerAsDocument)
	if problem != "" {
		r.toolMessage(c.ID, malformed("%s", problem).Output)
		return answer{}, false, nil
	}
	if strings.TrimSpace(text) == "" {
		return answer{}, false, errEmptyAnswer
	}
	return answer{text: text, cited: cited, viaDone: true, stop: StopAnswered}, true, nil
}

// errEmptyAnswer fails a reflect whose final answer is empty.
var errEmptyAnswer = fmt.Errorf("%w: reflect: the model returned an empty answer", domain.ErrUnavailable)

// parseDone decodes done's arguments: the answer (or the document rendered to markdown) and every cited id.
// problem explains malformed arguments to the model.
func parseDone(raw json.RawMessage, asDocument bool) (text string, cited []string, problem string) {
	var a struct {
		Answer         *string       `json:"answer"`
		Document       *doneDocument `json:"document"`
		MemoryIDs      []string      `json:"memory_ids"`
		MentalModelIDs []string      `json:"mental_model_ids"`
		ObservationIDs []string      `json:"observation_ids"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", nil, fmt.Sprintf("done: arguments are not valid JSON for this tool: %v. Call done() again.", err)
	}
	switch {
	case asDocument && a.Document == nil:
		return "", nil, "done requires a 'document' with a 'sections' array. Call done() again."
	case asDocument:
		text = a.Document.markdown()
	case a.Answer == nil:
		return "", nil, "done requires an 'answer'. Call done() again."
	default:
		text = *a.Answer
	}
	cited = append(append(append(cited, a.MemoryIDs...), a.ObservationIDs...), a.MentalModelIDs...)
	return strings.TrimSpace(text), cited, ""
}

// callTool runs one retrieval call and appends its tool message; unknown tools and malformed calls go back
// to the model, a retrieval failure ends reflect.
func (r *run) callTool(ctx context.Context, c port.ToolCall) error {
	start := r.clock.Now()
	tool, ok := r.tools[c.Name]
	var res ToolResult
	if !ok {
		res = malformed("Tool '%s' is not available. Use only the tools provided to you.", c.Name)
	} else {
		var err error
		if res, err = tool.Call(ctx, r.st, c.Arguments); err != nil {
			return fmt.Errorf("reflect: %w", err)
		}
	}
	r.toolMessage(c.ID, res.Output)
	if res.Error == "" {
		r.outputs = append(r.outputs, prompts.ToolOutput{Tool: c.Name, Output: string(res.Output)})
	}
	r.st.ToolCalls = append(r.st.ToolCalls, domain.ToolCallTrace{
		Iteration: r.st.Iteration, Tool: c.Name, ArgumentsJSON: string(c.Arguments), OutputJSON: string(res.Output),
		Error: res.Error, DurationMS: r.clock.Now().Sub(start).Milliseconds(),
	})
	return nil
}

// toolMessage appends the tool message answering call id.
func (r *run) toolMessage(id string, out json.RawMessage) {
	r.messages = append(r.messages, port.ChatMessage{Role: port.ChatRoleTool, ToolCallID: id, Content: string(out)})
}

// finish asks for the answer as a done call when the conversation ends on tool results; otherwise, or if
// the model doesn't comply or the call fails, it synthesises the answer in a tool-less call. The closing done
// skips the evidence guard, as Hindsight's last iteration does.
func (r *run) finish(ctx context.Context, stop StopReason) (answer, error) {
	if n := len(r.messages); n == 0 || r.messages[n-1].Role != port.ChatRoleTool {
		return r.synthesize(ctx, stop)
	}
	prompt, err := prompts.Render(prompts.ReflectDoneRequest, &r.data)
	if err != nil {
		return answer{}, fmt.Errorf("reflect: %w", err)
	}
	r.messages = append(r.messages, port.ChatMessage{Role: port.ChatRoleUser, Content: prompt})
	resp, err := r.chat(ctx, prompts.ReflectDoneRequest, r.specs, ToolDone)
	switch {
	case err == nil:
	case ctx.Err() != nil, errors.Is(err, domain.ErrResourceExhausted):
		return answer{}, err
	case isContextOverflow(err):
		return r.synthesize(ctx, StopContextFull)
	default:
		// The closing turn is optional and retrieves nothing (Hindsight falls back the same way); some
		// OpenAI-compatible backends also reject a named tool_choice.
		return r.synthesize(ctx, stop)
	}
	for _, c := range resp.Message.ToolCalls {
		if normalizeName(c.Name) != ToolDone {
			continue
		}
		text, cited, problem := parseDone(c.Arguments, r.data.AnswerAsDocument)
		if problem != "" {
			break
		}
		if text == "" {
			return answer{}, errEmptyAnswer
		}
		return answer{text: text, cited: cited, viaDone: true, stop: stop}, nil
	}
	return r.synthesize(ctx, stop)
}

// synthesize answers from the tool results gathered so far in one tool-less call. On a context overflow it
// drops the older half of the results (marking the prompt) and tries again, until nothing is left.
func (r *run) synthesize(ctx context.Context, stop StopReason) (answer, error) {
	data := r.data
	data.Retrieved = r.outputs
	for {
		system, err := prompts.Render(prompts.ReflectFinal, &data)
		if err != nil {
			return answer{}, fmt.Errorf("reflect: %w", err)
		}
		user, err := prompts.Render(prompts.ReflectFinalUser, &data)
		if err != nil {
			return answer{}, fmt.Errorf("reflect: %w", err)
		}
		r.messages = []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: system}, {Role: port.ChatRoleUser, Content: user},
		}
		resp, err := r.chat(ctx, prompts.ReflectFinal, nil, "")
		switch {
		case err != nil && isContextOverflow(err) && len(data.Retrieved) > 0:
			data.Retrieved = data.Retrieved[len(data.Retrieved)/2+len(data.Retrieved)%2:]
			data.Omitted, stop = true, StopContextFull
			continue
		case err != nil:
			return answer{}, err
		}
		text := strings.TrimSpace(resp.Message.Content)
		if text == "" {
			return answer{}, errEmptyAnswer
		}
		return answer{text: text, stop: stop}, nil
	}
}
