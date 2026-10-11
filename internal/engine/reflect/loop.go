package reflect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
)

// isContextOverflow reports whether a chat error is a context-window overflow: the gateway classifies
// provider overflow replies as domain.ErrContextOverflow (CCR WP-11-1, decision 1101).
func isContextOverflow(err error) bool {
	return errors.Is(err, domain.ErrContextOverflow)
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
	llm         ai.LLM
	clock       clock.Clock
	binding     domain.ModelBinding
	st          *agentState
	tools       map[string]Tool
	specs       []ai.ToolSpec
	data        prompts.ReflectData
	maxIter     int
	maxTotal    *int64
	requireEv   bool
	messages    []ai.ChatMessage
	outputs     []prompts.ToolOutput
	sawToolCall bool
	retried     bool
	callIDs     map[string]bool
	// instructions are the rendered model-facing tool errors (reflect_tool_error_* templates).
	instructions toolInstructions
}

// toolInstructions are the model-facing tool error texts, rendered once per run for the offered tools.
type toolInstructions struct {
	searchFirst string
	unknown     string
	done        string
}

// renderInstructions renders the tool error templates with the run's prompt data.
func renderInstructions(data *prompts.ReflectData) (toolInstructions, error) {
	var in toolInstructions
	for name, dst := range map[string]*string{
		prompts.ReflectToolErrorSearchFirst: &in.searchFirst,
		prompts.ReflectToolErrorUnknown:     &in.unknown,
		prompts.ReflectToolErrorDone:        &in.done,
	} {
		out, err := prompts.Render(name, data)
		if err != nil {
			return in, fmt.Errorf("reflect: %w", err)
		}
		*dst = strings.TrimSpace(out)
	}
	return in, nil
}

// loop runs up to maxIter tool-calling turns, then a closing done turn and, failing that, a tool-less
// final synthesis.
func (r *run) loop(ctx context.Context) (answer, error) {
	for i := 1; i <= r.maxIter; i++ {
		r.st.Iteration = i
		resp, err := r.turn(ctx)
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

// turn makes one loop turn. Until the model's first tool call the turn requires a tool call, and a prose reply
// is retried once (per run) before reflect concludes the model can't drive tools.
func (r *run) turn(ctx context.Context) (ai.ChatResponse, error) {
	if r.sawToolCall {
		return r.chat(ctx, prompts.ReflectSystem, r.specs, "auto")
	}
	resp, err := r.chat(ctx, prompts.ReflectSystem, r.specs, "required")
	if err != nil || len(resp.Message.ToolCalls) > 0 || r.retried {
		return resp, err
	}
	r.retried = true
	return r.chat(ctx, prompts.ReflectSystem, r.specs, "required")
}

// chat makes one REFLECT chat call over the conversation and charges its usage.
func (r *run) chat(ctx context.Context, name string, tools []ai.ToolSpec, choice string) (ai.ChatResponse, error) {
	resp, err := r.llm.Chat(ctx, ai.ChatRequest{
		Slot: domain.ModelSlotReflect, Binding: r.binding, Name: name, Messages: r.messages, Tools: tools,
		ToolChoice: choice,
	})
	if uerr := addUsage(r.st, resp.Usage, r.maxTotal); uerr != nil {
		return ai.ChatResponse{}, uerr
	}
	if err != nil {
		return ai.ChatResponse{}, fmt.Errorf("reflect: chat: %w", err)
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
func (r *run) uniqueCalls(calls []ai.ToolCall) []ai.ToolCall {
	out := make([]ai.ToolCall, len(calls))
	for i, c := range calls {
		c.Name = normalizeName(c.Name)
		c.ID = strings.TrimSpace(c.ID)
		for n := len(r.callIDs) + 1; c.ID == "" || r.callIDs[c.ID]; n++ {
			c.ID = fmt.Sprintf("call_%d", n)
		}
		r.callIDs[c.ID] = true
		out[i] = c
	}
	return out
}

// handle runs one turn's tool calls. A done call wins: the turn's other calls are dropped.
func (r *run) handle(ctx context.Context, calls []ai.ToolCall) (answer, bool, error) {
	calls = r.uniqueCalls(calls)
	for _, c := range calls {
		if c.Name == ToolDone {
			return r.handleDone(c)
		}
	}
	r.messages = append(r.messages, ai.ChatMessage{Role: ai.ChatRoleAssistant, ToolCalls: calls})
	for _, c := range calls {
		if err := r.callTool(ctx, c); err != nil {
			return answer{}, false, err
		}
	}
	return answer{}, false, nil
}

// handleDone accepts a done call, or sends it back: before any retrieval (require_evidence) and when its
// arguments are malformed. A present but blank answer fails reflect.
func (r *run) handleDone(c ai.ToolCall) (answer, bool, error) {
	r.messages = append(r.messages, ai.ChatMessage{Role: ai.ChatRoleAssistant, ToolCalls: []ai.ToolCall{c}})
	if r.requireEv && !r.st.Searched {
		r.toolMessage(c.ID, malformed("%s", r.instructions.searchFirst).Output)
		return answer{}, false, nil
	}
	text, cited, problem := parseDone(c.Arguments, r.data.AnswerAsDocument)
	if problem != "" {
		r.toolMessage(c.ID, malformed("%s. %s", problem, r.instructions.done).Output)
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
// problem says briefly what is wrong; the caller adds the retry instruction.
func parseDone(raw json.RawMessage, asDocument bool) (text string, cited []string, problem string) {
	var a struct {
		Answer         *string       `json:"answer"`
		Document       *doneDocument `json:"document"`
		MemoryIDs      []string      `json:"memory_ids"`
		MentalModelIDs []string      `json:"mental_model_ids"`
		ObservationIDs []string      `json:"observation_ids"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", nil, fmt.Sprintf("done: arguments are not valid JSON for this tool: %v", err)
	}
	switch {
	case asDocument && a.Document == nil:
		return "", nil, "done: missing 'document'"
	case asDocument:
		text = a.Document.markdown()
	case a.Answer == nil:
		return "", nil, "done: missing 'answer'"
	default:
		text = *a.Answer
	}
	cited = append(append(append(cited, a.MemoryIDs...), a.ObservationIDs...), a.MentalModelIDs...)
	return strings.TrimSpace(text), cited, ""
}

// callTool runs one retrieval call and appends its tool message; unknown tools and malformed calls go back
// to the model, a retrieval failure ends reflect.
func (r *run) callTool(ctx context.Context, c ai.ToolCall) error {
	start := r.clock.Now()
	tool, ok := r.tools[c.Name]
	var res toolResult
	if !ok {
		res = malformed("Tool '%s' is not available. %s", c.Name, r.instructions.unknown)
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
	r.messages = append(r.messages, ai.ChatMessage{Role: ai.ChatRoleTool, ToolCallID: id, Content: string(out)})
}

// finish asks for the answer as a done call when the conversation ends on tool results; otherwise, or if
// the model doesn't comply or the call fails, it synthesises the answer in a tool-less call. The evidence
// guard holds here too: with require_evidence and no retrieval yet, one turn is forced to call a search tool;
// if that retrieves nothing either, no done is accepted and the answer is synthesised.
func (r *run) finish(ctx context.Context, stop StopReason) (answer, error) {
	if r.requireEv && !r.st.Searched {
		if ans, ended, err := r.ensureEvidence(ctx, stop); ended || err != nil {
			return ans, err
		}
	}
	if n := len(r.messages); n == 0 || r.messages[n-1].Role != ai.ChatRoleTool {
		return r.synthesize(ctx, stop)
	}
	prompt, err := prompts.Render(prompts.ReflectDoneRequest, &r.data)
	if err != nil {
		return answer{}, fmt.Errorf("reflect: %w", err)
	}
	r.messages = append(r.messages, ai.ChatMessage{Role: ai.ChatRoleUser, Content: prompt})
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

// ensureEvidence forces one retrieval turn; ended reports that the run ended there (a synthesis because
// nothing was retrieved or the context overflowed, or an error).
func (r *run) ensureEvidence(ctx context.Context, stop StopReason) (ans answer, ended bool, err error) {
	if err := r.forceRetrieval(ctx); err != nil {
		if isContextOverflow(err) {
			ans, err = r.synthesize(ctx, StopContextFull)
			return ans, true, err
		}
		return answer{}, true, err
	}
	if !r.st.Searched {
		ans, err = r.synthesize(ctx, stop)
		return ans, true, err
	}
	return answer{}, false, nil
}

// forceRetrieval makes one turn whose tool choice is an offered search tool (recall, else search_observations,
// else search_mental_models) and runs the calls it returns, except done.
func (r *run) forceRetrieval(ctx context.Context) error {
	var name string
	for _, n := range []string{ToolRecall, ToolSearchObservations, ToolSearchMentalModels} {
		if _, ok := r.tools[n]; ok {
			name = n
			break
		}
	}
	if name == "" {
		return nil
	}
	resp, err := r.chat(ctx, prompts.ReflectSystem, r.specs, name)
	if err != nil {
		return err
	}
	var calls []ai.ToolCall
	for _, c := range resp.Message.ToolCalls {
		if normalizeName(c.Name) != ToolDone {
			calls = append(calls, c)
		}
	}
	if len(calls) == 0 {
		return nil
	}
	_, _, err = r.handle(ctx, calls)
	return err
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
		r.messages = []ai.ChatMessage{
			{Role: ai.ChatRoleSystem, Content: system}, {Role: ai.ChatRoleUser, Content: user},
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
