package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/prompts"
	"memd/internal/reflect"
	"slices"
	"strings"
)

// agentic is the REFRESH_STRATEGY_AGENTIC synthesizer: the reflect loop answering as a document over the
// model's scope and window. A delta runs the loop over the window only and integrates the evidence it
// cites into the current document with the delta call (the loop's own draft is not shown, as in
// Hindsight #5272).
type agentic struct {
	rf      *reflect.Agent
	planner deltaPlanner
}

var _ synthesizer = (*agentic)(nil)

// newAgentic returns the agentic synthesizer over rf; d makes the delta call.
func newAgentic(rf *reflect.Agent, d modelDeps) *agentic {
	return &agentic{rf: rf, planner: deltaPlanner{d: d}}
}

// Strategy is REFRESH_STRATEGY_AGENTIC.
func (*agentic) Strategy() domain.RefreshStrategy { return domain.RefreshStrategyAgentic }

// Full runs the reflect loop over everything in scope with seq <= upTo.
func (a *agentic) Full(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (draft, error) {
	req, err := a.request(m, cfg, domain.SeqUpTo(0, upTo))
	if err != nil {
		return draft{}, err
	}
	res, err := a.reflect(ctx, req)
	if err != nil {
		return draft{}, err
	}
	d := draft{
		ContentMD: strings.TrimSpace(res.Text), SourceIDs: unitIDs(res.BasedOn.Memories), Usage: res.Usage,
		ToolCalls: toolCalls(res), FactsRetrieved: len(res.BasedOn.Memories),
	}
	switch {
	case d.ContentMD == "" && len(res.BasedOn.Memories) == 0:
		return d, errNoNewFacts
	case len(res.BasedOn.Memories) == 0:
		// An answer that cites nothing is not grounded in the scope: fail rather than write it with no
		// sources (which would also prune source_ids and advance the watermark).
		return d, fmt.Errorf("%w: mentalmodel: no_citations: the reflect answer cites no memories",
			domain.ErrUnavailable)
	}
	return d, nil
}

// Delta runs the reflect loop over (LastSeqSeen, upTo] and integrates the memories it cites.
func (a *agentic) Delta(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64,
) (deltaDraft, error) {
	req, err := a.request(m, cfg, domain.SeqUpTo(m.LastSeqSeen, upTo))
	if err != nil {
		return deltaDraft{ContentMD: m.ContentMD}, err
	}
	res, err := a.reflect(ctx, req)
	if err != nil {
		return deltaDraft{ContentMD: m.ContentMD}, err
	}
	if len(res.BasedOn.Memories) == 0 {
		return deltaDraft{ContentMD: m.ContentMD, Usage: res.Usage, ToolCalls: toolCalls(res)}, errNoNewFacts
	}
	d, err := a.planner.plan(ctx, m, cfg, observationsFirst(res.BasedOn.Memories))
	d.Usage = append(slices.Clone(res.Usage), d.Usage...)
	d.ToolCalls = toolCalls(res)
	return d, err
}

// reflect runs one reflect loop; with no agent wired the agentic strategy fails.
func (a *agentic) reflect(ctx context.Context, req reflect.Request) (reflect.Result, error) {
	if a.rf == nil {
		return reflect.Result{}, fmt.Errorf("%w: mentalmodel: no reflect agent wired", domain.ErrFailedPrecondition)
	}
	res, err := a.rf.Reflect(ctx, req)
	if err != nil {
		return res, fmt.Errorf("mentalmodel: reflect: %w", err)
	}
	return res, nil
}

// request is the reflect request of a refresh over window: the model's scope, budget and exclusions,
// answering as a document, with the rendered mm_refresh_context as its context. The model itself is always
// excluded from the page tools.
func (a *agentic) request(
	m domain.MentalModel, cfg domain.BankConfig, window domain.SeqRange,
) (reflect.Request, error) {
	scope := resolveScope(m)
	facts, outputs := true, false
	req := reflect.Request{
		Bank: m.Bank, Query: m.Spec.SourceQuery, Tags: scope.tagFilter(), Budget: budget(m),
		Include:   reflect.Include{BasedOn: &facts, Trace: true, ToolOutputs: &outputs},
		Overrides: &cfg, Seq: window, FactTypes: scope.FactTypes,
		ExcludeMentalModels:   m.Spec.ExcludeMentalModels,
		ExcludeMentalModelIDs: appendUnique(slices.Clone(m.Spec.ExcludeMentalModelIDs), m.Spec.ID),
		RecallInclude: domain.RecallInclude{
			Chunks: m.Spec.IncludeChunks, ChunksMaxTokens: m.Spec.RecallChunksMaxTokens,
		},
		AsDocument: true,
	}
	if n := maxContentTokens(m, cfg); n > 0 {
		req.MaxTokens = &n
	}
	var err error
	req.Context, err = refreshContext(m, window)
	return req, err
}

// refreshContext renders the reflect context of an agentic refresh over window: the document's name and
// whether the loop sees only the delta window.
func refreshContext(m domain.MentalModel, window domain.SeqRange) (string, error) {
	out, err := prompts.Render(prompts.MentalModelRefreshContext,
		prompts.RefreshContextData{Name: m.Spec.Name, Delta: window.After > 0})
	if err != nil {
		return "", fmt.Errorf("mentalmodel: refresh context: %w", err)
	}
	return out, nil
}

// toolCalls returns the loop's tool calls, if traced.
func toolCalls(res reflect.Result) []domain.ToolCallTrace {
	if res.Trace == nil {
		return nil
	}
	return res.Trace.ToolCalls
}

// observationsFirst orders units with observations before raw facts, keeping order within each group.
func observationsFirst(units []domain.Unit) []domain.Unit {
	out := make([]domain.Unit, 0, len(units))
	for _, u := range units {
		if u.Type == domain.FactTypeObservation {
			out = append(out, u)
		}
	}
	for _, u := range units {
		if u.Type != domain.FactTypeObservation {
			out = append(out, u)
		}
	}
	return out
}

// appendUnique appends id when ids does not hold it yet.
func appendUnique(ids []domain.MentalModelID, id domain.MentalModelID) []domain.MentalModelID {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}
