package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jsonschema"
	"memd/internal/prompts"
	"memd/internal/recall"
	"memd/internal/store"
	"slices"
	"strings"
)

// Defaults when the effective config leaves a reflect knob unset (the server defaults normally set them).
const (
	defaultMaxIterations   = 10
	defaultMaxAnswerTokens = 4096
)

// Deps are the Agent's dependencies.
type Deps struct {
	// DB reads the bank's name and stored directives (a short read-only transaction), and serves expand through
	// DB.Read.
	DB store.DB
	// Config resolves the effective config (server < tenant < bank < request).
	Config *config.Resolver
	// Router picks the REFLECT and STRUCTURED_OUTPUT bindings.
	Router *ai.Router
	// LLM serves the tool-calling turns, the final synthesis and the structured-output pass.
	LLM ai.LLM
	// Clock gives the current time (default query time, durations).
	Clock clock.Clock
	// Recall serves the recall, search_observations and page tools; nil = only expand is offered.
	Recall *recall.Engine
}

// Agent is the reflect agent: a tool-calling loop over recall, mental-model pages and expand that answers a question
// with citations (Reflect), plus the structured-output projection of a finished document (Project). It is safe for
// concurrent use; never call it inside DB.InTx.
type Agent struct {
	d     Deps
	tools map[string]tool
}

// New returns the Agent over d; it builds its tools from d.Recall and d.DB.
func New(d Deps) *Agent {
	ts := newTools(d.Recall, d.DB.Read())
	tools := make(map[string]tool, len(ts))
	for _, t := range ts {
		tools[t.Name()] = t
	}
	return &Agent{d: d, tools: tools}
}

// Reflect runs up to max_iterations (default 10 at MID); the agent must retrieve before done; citations it never
// retrieved are dropped; past max_total_tokens it stops with domain.ErrMaxTotalTokens; an optional
// structured-output pass projects the answer onto the request's schema.
func (r *Agent) Reflect(ctx context.Context, req Request) (Result, error) {
	start := r.d.Clock.Now()
	if err := validate(req); err != nil {
		return Result{}, err
	}
	if err := r.d.Recall.CheckArms(req.Arms); err != nil {
		return Result{}, fmt.Errorf("reflect: %w", err)
	}
	var overrides []domain.BankConfig
	if req.Overrides != nil {
		overrides = append(overrides, *req.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, req.Bank, overrides...)
	if err != nil {
		return Result{}, fmt.Errorf("reflect: %w", err)
	}
	bankName, stored, err := r.readBank(ctx, req.Bank)
	if err != nil {
		return Result{}, err
	}
	applied := domain.SelectDirectives(stored, req.Tags, req.ApplyAllDirectives)
	directives := append(slices.Clone(applied), req.ExtraDirectives...)
	rn, err := r.newRun(req, cfg, directives, bankName)
	if err != nil {
		return Result{}, err
	}
	ans, err := rn.loop(ctx)
	if err != nil {
		return Result{}, err
	}
	res := r.result(rn, ans, directiveIDs(applied))
	if req.ResponseSchemaJSON != "" {
		if err := r.structured(ctx, rn, cfg, &res); err != nil {
			return Result{}, err
		}
	}
	res.Usage = rn.st.Usage
	res.Trace.DurationMS = r.d.Clock.Now().Sub(start).Milliseconds()
	return res, nil
}

// validate rejects a request reflect can't run: no query, a malformed tag filter or response schema.
func validate(req Request) error {
	if strings.TrimSpace(req.Query) == "" {
		return fmt.Errorf("%w: reflect needs a query", domain.ErrInvalidArgument)
	}
	if err := req.Tags.Validate(); err != nil {
		return fmt.Errorf("reflect: %w", err)
	}
	if req.ResponseSchemaJSON == "" {
		return nil
	}
	if _, err := jsonschema.Compile(json.RawMessage(req.ResponseSchemaJSON)); err != nil {
		return fmt.Errorf("reflect: response_schema_json: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.ResponseSchemaJSON), &obj); err != nil {
		return fmt.Errorf("%w: response_schema_json must be a JSON object", domain.ErrInvalidArgument)
	}
	return nil
}

// readBank reads the bank's display name and its active directives in one short read-only transaction.
func (r *Agent) readBank(ctx context.Context, b domain.BankRef) (string, []domain.Directive, error) {
	var name string
	var ds []domain.Directive
	err := r.d.DB.InTx(ctx, func(tx store.Tx) error {
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		name = bank.Name
		ds, err = tx.Directives().List(ctx, b, true)
		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("reflect: read bank %s/%s: %w", b.Tenant, b.Bank, err)
	}
	return name, ds, nil
}

// effectiveBudget is the request budget; unspecified = low (the Reflect RPC default).
func effectiveBudget(b domain.Budget) domain.Budget {
	switch b {
	case domain.BudgetMid, domain.BudgetHigh:
		return b
	default:
		return domain.BudgetLow
	}
}

// maxIterations scales ReflectConfig.MaxIterations (the BUDGET_MID value, default 10) by budget: LOW halves,
// HIGH doubles, never below 1.
func maxIterations(cfg domain.BankConfig, b domain.Budget) int {
	base := defaultMaxIterations
	if cfg.Reflect != nil && cfg.Reflect.MaxIterations != nil {
		base = *cfg.Reflect.MaxIterations
	}
	switch effectiveBudget(b) {
	case domain.BudgetHigh:
		base *= 2
	case domain.BudgetLow:
		base /= 2
	}
	return max(1, base)
}

// newRun prepares the loop: binding, tools offered, prompt data, system and user messages.
func (r *Agent) newRun(
	req Request, cfg domain.BankConfig, directives []domain.Directive, bankName string,
) (*run, error) {
	binding, err := r.d.Router.Resolve(cfg, domain.ModelSlotReflect)
	if err != nil {
		return nil, fmt.Errorf("reflect: %w", err)
	}
	queryTime := r.d.Clock.Now()
	if req.QueryTime != nil {
		queryTime = *req.QueryTime
	}
	tools := r.offered(cfg, req.ExcludeMentalModels, req.FactTypes)
	data := promptData(cfg, directives, toolNames(tools), queryTime)
	data.BankName, data.Context, data.Query = bankName, req.Context, req.Query
	data.Budget, data.AnswerAsDocument = string(effectiveBudget(req.Budget)), req.AsDocument
	data.MaxTokens = answerTokens(cfg, req.MaxTokens)
	specs, err := toolSpecs(tools, &data)
	if err != nil {
		return nil, err
	}
	system, err := prompts.Render(prompts.ReflectSystem, &data)
	if err != nil {
		return nil, fmt.Errorf("reflect: %w", err)
	}
	instructions, err := renderInstructions(&data)
	if err != nil {
		return nil, err
	}
	rn := &run{
		llm: r.d.LLM, clock: r.d.Clock, binding: binding, tools: tools, specs: specs, data: data,
		maxIter: maxIterations(cfg, req.Budget), callIDs: map[string]bool{}, requireEv: true, instructions: instructions,
		st: &agentState{Request: req, Config: cfg, QueryTime: queryTime},
		messages: []ai.ChatMessage{
			{Role: ai.ChatRoleSystem, Content: system}, {Role: ai.ChatRoleUser, Content: req.Query},
		},
	}
	if cfg.Reflect != nil {
		rn.maxTotal = cfg.Reflect.MaxTotalTokens
		if cfg.Reflect.RequireEvidence != nil {
			rn.requireEv = *cfg.Reflect.RequireEvidence
		}
	}
	if rn.requireEv && !canSearch(tools) {
		return nil, fmt.Errorf("%w: reflect requires evidence but no search tool is offered "+
			"(reflect.enabled_tools, request types or excluded mental models leave none)", domain.ErrFailedPrecondition)
	}
	return rn, nil
}

// canSearch reports whether tools include a search tool that can satisfy the evidence guard.
func canSearch(tools map[string]tool) bool {
	for _, name := range []string{ToolSearchMentalModels, ToolSearchObservations, ToolRecall} {
		if _, ok := tools[name]; ok {
			return true
		}
	}
	return false
}

// answerTokens is the answer's soft length target: the request's max_tokens, else max_answer_tokens.
func answerTokens(cfg domain.BankConfig, reqMax *int) int {
	switch {
	case reqMax != nil && *reqMax > 0:
		return *reqMax
	case cfg.Reflect != nil && cfg.Reflect.MaxAnswerTokens != nil:
		return *cfg.Reflect.MaxAnswerTokens
	default:
		return defaultMaxAnswerTokens
	}
}

// offered returns the retrieval tools this reflect offers: wired, enabled by the config, allowed by the
// request's fact types, and no page tools when mental models are excluded.
func (r *Agent) offered(cfg domain.BankConfig, excludePages bool, types []domain.FactType) map[string]tool {
	enabled := toolSet(cfg, !excludePages, types)
	out := map[string]tool{}
	for name := range enabled {
		if t, ok := r.tools[name]; ok {
			out[name] = t
		}
	}
	return out
}

// toolNames is the name set of tools.
func toolNames(tools map[string]tool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for name := range tools {
		out[name] = true
	}
	return out
}

// toolSpecs declares the offered tools to the model in prompt order, then the done variant that fits:
// the document form for AsDocument, the directive form when some directive has rules.
func toolSpecs(tools map[string]tool, data *prompts.ReflectData) ([]ai.ToolSpec, error) {
	decl, err := toolSchemas()
	if err != nil {
		return nil, err
	}
	var specs []ai.ToolSpec
	for _, name := range retrievalTools {
		if t, ok := tools[name]; ok {
			specs = append(specs, ai.ToolSpec{Name: name, Description: decl[name].Description, Parameters: t.Schema()})
		}
	}
	done, err := doneSpec(decl[ToolDone], data)
	if err != nil {
		return nil, err
	}
	return append(specs, done), nil
}

// doneSpec returns the done tool declaration for data.
func doneSpec(plain ai.ToolSpec, data *prompts.ReflectData) (ai.ToolSpec, error) {
	rules := hasRules(data.Directives)
	var asset string
	switch {
	case data.AnswerAsDocument && rules:
		asset = prompts.ReflectToolDoneDocumentDirectives
	case data.AnswerAsDocument:
		asset = prompts.ReflectToolDoneDocument
	case rules:
		asset = prompts.ReflectToolDoneDirectives
	default:
		return plain, nil
	}
	raw, err := prompts.DoneTool(asset, data)
	if err != nil {
		return ai.ToolSpec{}, fmt.Errorf("reflect: %w", err)
	}
	var s struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return ai.ToolSpec{}, fmt.Errorf("reflect: %s: %w", asset, err)
	}
	return ai.ToolSpec{Name: s.Name, Description: s.Description, Parameters: s.Parameters}, nil
}

// result assembles the answer, its evidence and the trace (without duration and usage).
func (r *Agent) result(rn *run, ans answer, directives []domain.DirectiveID) Result {
	req := rn.st.Request
	res := Result{Text: ans.text}
	if req.Include.BasedOn == nil || *req.Include.BasedOn {
		if ans.viaDone {
			res.BasedOn = citedBasedOn(rn.st, ans.cited)
		} else {
			res.BasedOn = allBasedOn(rn.st)
		}
	}
	res.BasedOn.DirectiveIDs = directives
	res.Trace = &Trace{
		Iterations: rn.st.Iteration, StopReason: ans.stop, DirectiveIDs: res.BasedOn.DirectiveIDs,
	}
	if req.Include.Trace {
		outputs := req.Include.ToolOutputs == nil || *req.Include.ToolOutputs
		for _, c := range rn.st.ToolCalls {
			if !outputs {
				c.OutputJSON = ""
			}
			res.Trace.ToolCalls = append(res.Trace.ToolCalls, c)
		}
	}
	return res
}

// structured runs the structured-output pass. Its failure is reported in StructuredOutputError and the answer
// is still returned; only the token budget and the caller's ctx fail the call.
func (r *Agent) structured(ctx context.Context, rn *run, cfg domain.BankConfig, res *Result) error {
	out, usage, err := project(ctx, r.d.LLM, r.d.Router, cfg, json.RawMessage(rn.st.Request.ResponseSchemaJSON), res.Text)
	if uerr := addUsage(rn.st, usage, rn.maxTotal); uerr != nil {
		return uerr
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("reflect: structured output: %w", err)
		}
		res.StructuredOutputError = err.Error()
		return nil
	}
	res.StructuredOutputJSON = out
	return nil
}
