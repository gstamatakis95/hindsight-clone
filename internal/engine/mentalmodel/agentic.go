package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/reflect"
	"memd/internal/port"
	"memd/internal/prompts"
	"slices"
	"strings"
)

// Reflector is the part of reflect.Reflector the agentic synthesizer consumes; reflect.Reflector
// satisfies it.
type Reflector interface {
	// Reflect runs one reflect loop (answering as a document for refreshes).
	Reflect(ctx context.Context, req reflect.ReflectRequest) (reflect.ReflectResult, error)
}

// agentic is the REFRESH_STRATEGY_AGENTIC Synthesizer: the reflect loop answering as a document over the
// model's scope and window. A delta runs the loop over the window only and integrates the evidence it
// cites into the current document with MENTAL_MODEL_DELTA (the loop's own draft is not shown, as in
// Hindsight #5272).
type agentic struct {
	rf      Reflector
	planner deltaPlanner
}

var _ Synthesizer = (*agentic)(nil)

// NewAgenticSynthesizer returns the agentic Synthesizer; d makes the delta's MENTAL_MODEL_DELTA call.
func NewAgenticSynthesizer(rf Reflector, d ModelDeps) Synthesizer {
	return &agentic{rf: rf, planner: deltaPlanner{d: d}}
}

// Strategy is REFRESH_STRATEGY_AGENTIC.
func (*agentic) Strategy() domain.RefreshStrategy { return domain.RefreshStrategyAgentic }

// Full runs the reflect loop over everything in scope with seq <= upTo.
func (a *agentic) Full(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (Draft, error) {
	res, err := a.rf.Reflect(ctx, a.request(m, cfg, port.SeqUpTo(0, upTo)))
	if err != nil {
		return Draft{}, fmt.Errorf("mentalmodel: reflect: %w", err)
	}
	d := Draft{
		ContentMD: strings.TrimSpace(res.Text), SourceIDs: unitIDs(res.BasedOn.Memories), Usage: res.Usage,
		ToolCalls: toolCalls(res), FactsRetrieved: len(res.BasedOn.Memories),
	}
	switch {
	case d.ContentMD == "" && len(res.BasedOn.Memories) == 0:
		return d, ErrNoNewFacts
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
) (DeltaDraft, error) {
	res, err := a.rf.Reflect(ctx, a.request(m, cfg, port.SeqUpTo(m.LastSeqSeen, upTo)))
	if err != nil {
		return DeltaDraft{ContentMD: m.ContentMD}, fmt.Errorf("mentalmodel: reflect: %w", err)
	}
	if len(res.BasedOn.Memories) == 0 {
		return DeltaDraft{ContentMD: m.ContentMD, Usage: res.Usage, ToolCalls: toolCalls(res)}, ErrNoNewFacts
	}
	d, err := a.planner.plan(ctx, m, cfg, observationsFirst(res.BasedOn.Memories))
	d.Usage = append(slices.Clone(res.Usage), d.Usage...)
	d.ToolCalls = toolCalls(res)
	return d, err
}

// request is the reflect request of a refresh over window: the model's scope, budget and exclusions,
// answering as a document. The model itself is always excluded from the page tools.
func (a *agentic) request(m domain.MentalModel, cfg domain.BankConfig, window port.SeqRange) reflect.ReflectRequest {
	scope := resolveScope(m)
	t := m.Spec.Trigger
	facts, outputs := true, false
	req := reflect.ReflectRequest{
		Bank: m.Bank, Query: m.Spec.SourceQuery, Tags: scope.tagFilter(), Budget: budget(m),
		Include:   reflect.ReflectInclude{Facts: &facts, ToolCalls: true, ToolOutputs: &outputs},
		Overrides: &cfg, Seq: window, Types: scope.FactTypes,
		ExcludeMentalModels:   t.ExcludeMentalModels,
		ExcludeMentalModelIDs: appendUnique(slices.Clone(t.ExcludeMentalModelIDs), m.Spec.ID),
		RecallInclude:         domain.RecallInclude{Chunks: t.IncludeChunks, ChunksMaxTokens: t.RecallChunksMaxTokens},
		AsDocument:            true,
	}
	if n := maxContentTokens(m, cfg); n > 0 {
		req.MaxTokens = &n
	}
	// TODO(WP-11): once ReflectRequest.Context merges, set req.Context from refreshContext(m, window):
	// prompts.MentalModelRefreshContext with RefreshContextData{Name: m.Spec.Name, Delta: window.After > 0}.
	return req
}

// refreshContext renders the reflect context of an agentic refresh over window: the document's name and
// whether the loop sees only the delta window.
func refreshContext(m domain.MentalModel, window port.SeqRange) (string, error) {
	out, err := prompts.Render(prompts.MentalModelRefreshContext,
		prompts.RefreshContextData{Name: m.Spec.Name, Delta: window.After > 0})
	if err != nil {
		return "", fmt.Errorf("mentalmodel: refresh context: %w", err)
	}
	return out, nil
}

// toolCalls returns the loop's tool calls, if traced.
func toolCalls(res reflect.ReflectResult) []domain.ToolCallTrace {
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
func appendUnique(ids []port.MentalModelID, id port.MentalModelID) []port.MentalModelID {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}
