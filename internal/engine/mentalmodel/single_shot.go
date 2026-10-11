package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"memd/internal/recall"
	"memd/internal/store"
	"strings"
)

// Recaller is the part of recall.Engine the single-shot synthesizer consumes; recall.Engine
// satisfies it.
type Recaller interface {
	// Recall runs one recall request (bounded by RecallRequest.Seq for a stable snapshot).
	Recall(ctx context.Context, req recall.Request) (recall.Result, error)
}

// singleShot is the REFRESH_STRATEGY_SINGLE_SHOT Synthesizer: one recall over the scope (observations
// first), then one synthesize call (full) or one delta call (delta).
type singleShot struct {
	rc      Recaller
	d       ModelDeps
	planner deltaPlanner
}

var _ Synthesizer = (*singleShot)(nil)

// NewSingleShotSynthesizer returns the single-shot Synthesizer.
func NewSingleShotSynthesizer(rc Recaller, d ModelDeps) Synthesizer {
	return &singleShot{rc: rc, d: d, planner: deltaPlanner{d: d}}
}

// Strategy is REFRESH_STRATEGY_SINGLE_SHOT.
func (*singleShot) Strategy() domain.RefreshStrategy { return domain.RefreshStrategySingleShot }

// Full recalls everything in scope with seq <= upTo and writes the page in one synthesize call.
func (s *singleShot) Full(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64,
) (draft, error) {
	units, err := s.recall(ctx, m, cfg, domain.SeqUpTo(0, upTo))
	if err != nil {
		return draft{}, err
	}
	if len(units) == 0 {
		return draft{}, ErrNoNewFacts
	}
	binding, err := s.d.Router.Resolve(cfg, domain.ModelSlotMentalModel)
	if err != nil {
		return draft{}, fmt.Errorf("mentalmodel: synthesize model: %w", err)
	}
	system, user, err := s.render(ctx, m, cfg, units)
	if err != nil {
		return draft{}, err
	}
	resp, err := s.d.LLM.Chat(ctx, ai.ChatRequest{
		Slot: domain.ModelSlotMentalModel, Binding: binding, Name: prompts.SynthesizeSystem,
		Messages: []ai.ChatMessage{
			{Role: ai.ChatRoleSystem, Content: system}, {Role: ai.ChatRoleUser, Content: user},
		},
	})
	if err != nil {
		return draft{}, fmt.Errorf("mentalmodel: synthesize call: %w", err)
	}
	return draft{
		ContentMD: strings.TrimSpace(resp.Message.Content), SourceIDs: unitIDs(units),
		Usage: []domain.Usage{resp.Usage}, FactsRetrieved: len(units),
	}, nil
}

// Delta recalls what changed in scope since LastSeqSeen and integrates it with delta.
func (s *singleShot) Delta(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64,
) (deltaDraft, error) {
	units, err := s.recall(ctx, m, cfg, domain.SeqUpTo(m.LastSeqSeen, upTo))
	if err != nil {
		return deltaDraft{}, err
	}
	if len(units) == 0 {
		return deltaDraft{ContentMD: m.ContentMD}, ErrNoNewFacts
	}
	return s.planner.plan(ctx, m, cfg, units)
}

// recall runs one recall over m's scope and window; observations come back first.
func (s *singleShot) recall(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, window domain.SeqRange,
) ([]domain.Unit, error) {
	scope := resolveScope(m)
	req := recall.Request{
		Bank: m.Bank, Query: m.Spec.SourceQuery, Types: scope.FactTypes, Tags: scope.tagFilter(),
		Budget: budget(m), PreferObservations: true, Overrides: &cfg, Seq: window,
	}
	if cfg.Reflect != nil && cfg.Reflect.RecallMaxTokens != nil {
		req.MaxTokens = *cfg.Reflect.RecallMaxTokens
	}
	res, err := s.rc.Recall(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: recall: %w", err)
	}
	units := make([]domain.Unit, len(res.Results))
	for i, r := range res.Results {
		units[i] = r.Unit
	}
	return observationsFirst(units), nil
}

// render renders the synthesize system and user messages.
func (s *singleShot) render(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, units []domain.Unit,
) (system, user string, err error) {
	obs, facts := promptFacts(units)
	data := prompts.SynthesizeData{
		Name: m.Spec.Name, SourceQuery: m.Spec.SourceQuery, Mission: cfg.GetReflect().Mission,
		Observations: obs, Facts: facts, MaxTokens: maxContentTokens(m, cfg), Now: s.d.Clock.Now(),
	}
	if s.d.Directives != nil {
		if data.Directives, err = scopeDirectives(ctx, s.d.Directives, m); err != nil {
			return "", "", err
		}
	}
	if system, err = prompts.Render(prompts.SynthesizeSystem, data); err != nil {
		return "", "", fmt.Errorf("mentalmodel: render synthesize system: %w", err)
	}
	if user, err = prompts.Render(prompts.SynthesizeUser, data); err != nil {
		return "", "", fmt.Errorf("mentalmodel: render synthesize user: %w", err)
	}
	return system, user, nil
}

// budget is the trigger's budget, default mid.
func budget(m domain.MentalModel) domain.Budget {
	if m.Spec.Trigger.Budget == domain.BudgetUnspecified {
		return domain.BudgetMid
	}
	return m.Spec.Trigger.Budget
}

// scopeDirectives returns the directives synthesize applies to m: the bank's active directives that
// domain.SelectDirectives picks for the model's scope filter, the filter the agentic strategy hands reflect, so
// both strategies (and the reflect they mirror) apply the same directives. They are read in a short transaction.
func scopeDirectives(ctx context.Context, s store.DB, m domain.MentalModel) ([]domain.Directive, error) {
	var active []domain.Directive
	err := s.InTx(ctx, func(tx store.Tx) error {
		var err error
		active, err = tx.Directives().List(ctx, m.Bank, true)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: directives: %w", err)
	}
	return domain.SelectDirectives(active, resolveScope(m).tagFilter(), false), nil
}
