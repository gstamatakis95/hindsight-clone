package reflect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/port"
	"memd/internal/prompts"
	"slices"
	"strings"
	"time"
)

// Tool defaults and clamps (Hindsight's reflect tool defaults).
const (
	defaultRecallMaxTokens       = 2048
	defaultObservationsMaxTokens = 5000
	defaultReadPagesMaxTokens    = 6000
	defaultPageResults           = 5
	minToolTokens                = 1000
	maxToolTokens                = 16000
	maxFullPageTokens            = 4000
	snippetChars                 = 280
)

// DefaultTools returns the retrieval tools: recall and search_observations over rc, the page tools over pages
// and expand over r (units, source facts and chunks). A nil dependency leaves its tools out. The done tool is
// part of the loop, not this list.
func DefaultTools(rc Recaller, pages PageReader, r port.Reader) []Tool {
	var tools []Tool
	if pages != nil {
		tools = append(tools, &searchPagesTool{pages: pages}, &readPagesTool{pages: pages})
	}
	if rc != nil {
		tools = append(tools, &observationsTool{rc: rc}, &recallTool{rc: rc})
	}
	if r != nil {
		tools = append(tools, &expandTool{r: r})
	}
	return tools
}

// toolSchemas maps each tool name to its declaration in the reflect_tools asset.
func toolSchemas() (map[string]port.ToolSpec, error) {
	raw, err := prompts.JSON(prompts.ReflectTools)
	if err != nil {
		return nil, fmt.Errorf("reflect: %w", err)
	}
	var specs []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("reflect: reflect_tools: %w", err)
	}
	out := make(map[string]port.ToolSpec, len(specs))
	for _, s := range specs {
		out[s.Name] = port.ToolSpec{Name: s.Name, Description: s.Description, Parameters: s.Parameters}
	}
	return out, nil
}

// schemaOf returns a tool's parameter schema from the reflect_tools asset ({} when missing).
func schemaOf(name string) json.RawMessage {
	specs, err := toolSchemas()
	if err != nil {
		return json.RawMessage(`{}`)
	}
	if s, ok := specs[name]; ok {
		return s.Parameters
	}
	return json.RawMessage(`{}`)
}

// malformed returns the tool result that sends a malformed-call error back to the model.
func malformed(format string, args ...any) ToolResult {
	msg := fmt.Sprintf(format, args...)
	out, _ := json.Marshal(map[string]string{"error": msg})
	return ToolResult{Output: out, Error: msg}
}

// output marshals a tool's JSON output.
func output(v any) (ToolResult, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return ToolResult{}, fmt.Errorf("reflect: tool output: %w", err)
	}
	return ToolResult{Output: out}, nil
}

// decodeArgs decodes a tool call's raw arguments; a failure is reported to the model.
func decodeArgs(name string, raw json.RawMessage, v any) (ToolResult, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return malformed("%s: arguments are not valid JSON for this tool: %v", name, err), false
	}
	return ToolResult{}, true
}

// retrievalError classifies a dependency error; it always fails reflect (strict: it never answers from
// evidence it could not gather). A caller's ctx error and an already classified error (invalid argument,
// failed precondition, not found, unavailable) keep their class; anything else is UNAVAILABLE. The one
// argument error the model controls, an over-long query, is caught before the call (checkQuery).
func retrievalError(ctx context.Context, name string, err error) (ToolResult, error) {
	switch {
	case ctx.Err() != nil, errors.Is(err, domain.ErrInvalidArgument), errors.Is(err, domain.ErrFailedPrecondition),
		errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnavailable):
		return ToolResult{}, fmt.Errorf("reflect: %s: %w", name, err)
	default:
		return ToolResult{}, fmt.Errorf("%w: reflect: %s: %w", domain.ErrUnavailable, name, err)
	}
}

// tokenArg resolves a token-budget argument: the model's value clamped to [1000, 16000], else def.
func tokenArg(v *float64, def int) int {
	if v == nil || *v <= 0 {
		return def
	}
	return min(max(int(*v), minToolTokens), maxToolTokens)
}

// maxQueryTokens is recall's query limit (RecallRequest.Query: max 500 tokens).
const maxQueryTokens = 500

// checkQuery returns the malformed-call result for a missing or over-long query; ok when the query is usable.
func checkQuery(tool, query string) (ToolResult, bool) {
	switch {
	case strings.TrimSpace(query) == "":
		return malformed("%s requires a query parameter", tool), false
	case estimateTokens(query) > maxQueryTokens:
		return malformed("%s: the query is too long (max %d tokens); search with a shorter query", tool,
			maxQueryTokens), false
	}
	return ToolResult{}, true
}

// estimateTokens approximates a text's token count as one token per four bytes (decision 1104).
func estimateTokens(s string) int { return (len(s) + 3) / 4 }

// memoryOut is one memory or observation as the tools show it to the model.
type memoryOut struct {
	ID            port.UnitID       `json:"id"`
	Text          string            `json:"text"`
	FactType      domain.FactType   `json:"fact_type"`
	Context       string            `json:"context,omitempty"`
	Entities      []string          `json:"entities,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	OccurredStart *time.Time        `json:"occurred_start,omitempty"`
	OccurredEnd   *time.Time        `json:"occurred_end,omitempty"`
	MentionedAt   *time.Time        `json:"mentioned_at,omitempty"`
	SourceFactIDs []port.UnitID     `json:"source_fact_ids,omitempty"`
	IsStale       *bool             `json:"is_stale,omitempty"`
}

// toMemoryOut converts a unit for a tool output.
func toMemoryOut(u domain.Unit) memoryOut {
	return memoryOut{
		ID: u.ID, Text: u.Text, FactType: u.Type, Context: u.Context, Entities: u.Entities, Tags: u.Tags,
		Metadata: u.Metadata, OccurredStart: u.OccurredStart, OccurredEnd: u.OccurredEnd,
		MentionedAt: u.MentionedAt, SourceFactIDs: u.SourceIDs,
	}
}

// remember records retrieved units in the agent state.
func remember(st *AgentState, units ...domain.Unit) {
	if st.RetrievedUnits == nil {
		st.RetrievedUnits = map[port.UnitID]domain.Unit{}
	}
	for _, u := range units {
		st.RetrievedUnits[u.ID] = u
	}
}

// recallRequest builds the recall request a recall-backed tool sends.
func recallRequest(st *AgentState, query string, types []domain.FactType, maxTokens int) recall.RecallRequest {
	budget := domain.BudgetMid
	if st.Config.Reflect != nil && st.Config.Reflect.RecallBudget != domain.BudgetUnspecified {
		budget = st.Config.Reflect.RecallBudget
	}
	qt := st.QueryTime
	return recall.RecallRequest{
		Bank: st.Request.Bank, Query: query, Types: types, Tags: st.Request.Tags, Budget: budget,
		MaxTokens: maxTokens, QueryTime: &qt, Include: st.Request.RecallInclude, Overrides: st.Request.Overrides,
		Seq: st.Request.Seq,
	}
}

// queryArgs are the arguments of the query-taking tools.
type queryArgs struct {
	Query      string   `json:"query"`
	MaxTokens  *float64 `json:"max_tokens"`
	MaxResults *float64 `json:"max_results"`
}

// recallTool is the recall tool: a full recall over raw facts.
type recallTool struct{ rc Recaller }

var _ Tool = (*recallTool)(nil)

// Name implements Tool.
func (*recallTool) Name() string { return ToolRecall }

// Schema implements Tool.
func (*recallTool) Schema() json.RawMessage { return schemaOf(ToolRecall) }

// Call implements Tool.
func (t *recallTool) Call(ctx context.Context, st *AgentState, args json.RawMessage) (ToolResult, error) {
	var a queryArgs
	if res, ok := decodeArgs(ToolRecall, args, &a); !ok {
		return res, nil
	}
	if res, ok := checkQuery(ToolRecall, a.Query); !ok {
		return res, nil
	}
	def := defaultRecallMaxTokens
	if st.Config.Reflect != nil && st.Config.Reflect.RecallMaxTokens != nil {
		def = *st.Config.Reflect.RecallMaxTokens
	}
	res, err := t.rc.Recall(ctx, recallRequest(st, a.Query, factTypes(st.Request.Types), tokenArg(a.MaxTokens, def)))
	if err != nil {
		return retrievalError(ctx, ToolRecall, err)
	}
	st.Searched = true
	memories := make([]memoryOut, 0, len(res.Results))
	for _, m := range res.Results {
		remember(st, m.Unit)
		memories = append(memories, toMemoryOut(m.Unit))
	}
	out := map[string]any{"query": a.Query, "memories": memories}
	if len(res.Chunks) > 0 {
		chunks := map[port.ChunkID]any{}
		for id, c := range res.Chunks {
			chunks[id] = map[string]any{"chunk_text": c.Text, "chunk_index": c.Index, "truncated": c.Truncated}
		}
		out["chunks"] = chunks
	}
	return output(out)
}

// factTypes returns the raw-fact types the recall tool searches: world and experience for an unrestricted
// request, else the request's types (internal callers) minus observations, possibly none (then recall is not
// offered).
func factTypes(req []domain.FactType) []domain.FactType {
	if len(req) == 0 {
		return []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience}
	}
	var out []domain.FactType
	for _, t := range req {
		if t != domain.FactTypeObservation {
			out = append(out, t)
		}
	}
	return out
}

// observationsTool is search_observations: a recall over observations only.
type observationsTool struct{ rc Recaller }

var _ Tool = (*observationsTool)(nil)

// Name implements Tool.
func (*observationsTool) Name() string { return ToolSearchObservations }

// Schema implements Tool.
func (*observationsTool) Schema() json.RawMessage { return schemaOf(ToolSearchObservations) }

// Call implements Tool.
func (t *observationsTool) Call(ctx context.Context, st *AgentState, args json.RawMessage) (ToolResult, error) {
	var a queryArgs
	if res, ok := decodeArgs(ToolSearchObservations, args, &a); !ok {
		return res, nil
	}
	if res, ok := checkQuery(ToolSearchObservations, a.Query); !ok {
		return res, nil
	}
	def, entities := defaultObservationsMaxTokens, true
	if rc := st.Config.Reflect; rc != nil {
		if rc.SearchObservationsMaxTokens != nil {
			def = *rc.SearchObservationsMaxTokens
		}
		if rc.SearchObservationsIncludeEntities != nil {
			entities = *rc.SearchObservationsIncludeEntities
		}
	}
	req := recallRequest(st, a.Query, []domain.FactType{domain.FactTypeObservation}, tokenArg(a.MaxTokens, def))
	req.Include.Entities = &entities
	res, err := t.rc.Recall(ctx, req)
	if err != nil {
		return retrievalError(ctx, ToolSearchObservations, err)
	}
	st.Searched = true
	return observationsOutput(st, a.Query, res)
}

// observationsOutput renders search_observations' result and records the observations.
func observationsOutput(st *AgentState, query string, res recall.RecallResult) (ToolResult, error) {
	obs := make([]memoryOut, 0, len(res.Results))
	anyStale := false
	for _, m := range res.Results {
		remember(st, m.Unit)
		o := toMemoryOut(m.Unit)
		stale := m.Stale
		o.IsStale = &stale
		anyStale = anyStale || stale
		obs = append(obs, o)
	}
	out := map[string]any{"query": query, "count": len(obs), "observations": obs, "is_stale": anyStale}
	if len(res.Entities) > 0 {
		ents := make([]map[string]any, 0, len(res.Entities))
		for _, e := range res.Entities {
			ents = append(ents, map[string]any{
				"id": e.EntityID, "name": e.CanonicalName, "observation_ids": e.ObservationIDs,
			})
		}
		slices.SortFunc(ents, func(a, b map[string]any) int {
			return strings.Compare(fmt.Sprint(a["name"]), fmt.Sprint(b["name"]))
		})
		out["entities"] = ents
	}
	return output(out)
}
