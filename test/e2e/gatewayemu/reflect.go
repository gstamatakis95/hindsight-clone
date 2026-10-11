package gatewayemu

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Reflect tool names (internal/prompts/templates/reflect_tools.json).
const (
	toolDone = "done"
	// maxQueryRunes keeps the recall query well under recall's 500-token limit.
	maxQueryRunes = 400
)

// searchTools are the retrieval tools the emulator calls, in order of preference.
var searchTools = []string{"recall", "search_observations", "search_mental_models"}

// reflectTurn answers one turn of the reflect loop: a retrieval call while nothing has been retrieved, then
// done. A tool_choice naming done forces done.
func reflectTurn(req *chatRequest) (reply, error) {
	if forcedTool(req.ToolChoice) == toolDone || hasToolResult(req) {
		return doneCall(req)
	}
	tool := ""
	for _, name := range searchTools {
		if req.offers(name) {
			tool = name
			break
		}
	}
	if tool == "" {
		return doneCall(req)
	}
	query := []rune(strings.TrimSpace(req.firstUser()))
	if len(query) > maxQueryRunes {
		query = query[:maxQueryRunes]
	}
	args, err := json.Marshal(map[string]any{"reason": "emulator: retrieve evidence", "query": string(query)})
	if err != nil {
		return reply{}, emuErrorf("encode %s arguments: %v", tool, err)
	}
	return reply{calls: []toolCall{newCall(len(req.Messages), tool, string(args))}}, nil
}

// doneCall answers with done: the retrieved texts quoted, their ids cited, shaped by the offered done tool's
// parameters (answer or document, plus directive_compliance when declared).
func doneCall(req *chatRequest) (reply, error) {
	params, ok := req.toolParams(toolDone)
	if !ok {
		return reply{}, emuErrorf("reflect turn without a done tool")
	}
	mem, obs := retrieved(req)
	answer := quoteAnswer(append(append([]Item{}, mem...), obs...))
	args := map[string]any{"memory_ids": ids(mem), "observation_ids": ids(obs), "mental_model_ids": []string{}}
	props, _ := dig(params, "properties").(map[string]any)
	if _, ok := props["document"]; ok {
		args["document"] = map[string]any{"sections": []any{map[string]any{
			"heading": "", "level": 2, "blocks": answerBlocks(mem, obs),
		}}}
	} else {
		args["answer"] = answer
	}
	if _, ok := props["directive_compliance"]; ok {
		args["directive_compliance"] = "Every directive is followed: the answer only quotes retrieved memories."
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return reply{}, emuErrorf("encode done arguments: %v", err)
	}
	return reply{calls: []toolCall{newCall(len(req.Messages), toolDone, string(raw))}}, nil
}

// answerBlocks are the blocks of a document-mode answer: one per retrieved text, or the no-memories line.
func answerBlocks(mem, obs []Item) []string {
	var out []string
	for _, it := range append(append([]Item{}, mem...), obs...) {
		out = append(out, it.Text)
	}
	if len(out) == 0 {
		out = []string{noMemories}
	}
	return out
}

// retrieved collects the memories and observations of every tool message, deduplicated by id.
func retrieved(req *chatRequest) (memories, observations []Item) {
	seen := map[string]bool{}
	keep := func(items []Item, into *[]Item) {
		for _, it := range items {
			if !seen[it.ID] {
				seen[it.ID] = true
				*into = append(*into, it)
			}
		}
	}
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		mem, obs := toolItems(m.text())
		keep(mem, &memories)
		keep(obs, &observations)
	}
	return memories, observations
}

// ids returns the ids of items (never nil, so it encodes as []).
func ids(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// hasToolResult reports whether the conversation holds a tool message.
func hasToolResult(req *chatRequest) bool {
	for _, m := range req.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// offers reports whether the request offers tool name.
func (r *chatRequest) offers(name string) bool {
	_, ok := r.toolParams(name)
	return ok
}

// toolParams returns the decoded parameter schema of the offered tool name.
func (r *chatRequest) toolParams(name string) (any, bool) {
	for _, t := range r.Tools {
		if t.Function.Name == name {
			var params any
			_ = json.Unmarshal(t.Function.Parameters, &params)
			return params, true
		}
	}
	return nil, false
}

// forcedTool returns the tool a function-selector tool_choice names, or "".
func forcedTool(choice json.RawMessage) string {
	var sel struct {
		// Function names the forced tool.
		Function struct {
			// Name is the tool name.
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(choice, &sel) != nil {
		return ""
	}
	return sel.Function.Name
}

// newCall builds a tool call with an id unique within the conversation.
func newCall(turn int, name, args string) toolCall {
	c := toolCall{ID: fmt.Sprintf("call_emu_%d", turn), Type: "function"}
	c.Function.Name, c.Function.Arguments = name, args
	return c
}
