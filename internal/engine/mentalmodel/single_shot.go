package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/port"
	"memd/internal/prompts"
	"strings"
)

// Recaller is the part of recall.Recaller the single-shot synthesizer consumes; recall.Recaller
// satisfies it.
type Recaller interface {
	// Recall runs one recall request (bounded by RecallRequest.Seq for a stable snapshot).
	Recall(ctx context.Context, req recall.RecallRequest) (recall.RecallResult, error)
}

// singleShot is the REFRESH_STRATEGY_SINGLE_SHOT Synthesizer: one recall over the scope (observations
// first), then one SYNTHESIZE call (full) or one MENTAL_MODEL_DELTA call (delta).
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

// Full recalls everything in scope with seq <= upTo and writes the page in one SYNTHESIZE call.
func (s *singleShot) Full(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64,
) (draft, error) {
	units, err := s.recall(ctx, m, cfg, port.SeqUpTo(0, upTo))
	if err != nil {
		return draft{}, err
	}
	if len(units) == 0 {
		return draft{}, ErrNoNewFacts
	}
	binding, err := s.d.Router.Resolve(cfg, domain.ModelSlotSynthesize)
	if err != nil {
		return draft{}, fmt.Errorf("mentalmodel: synthesize model: %w", err)
	}
	system, user, err := s.render(ctx, m, cfg, units)
	if err != nil {
		return draft{}, err
	}
	resp, err := s.d.LLM.Chat(ctx, port.ChatRequest{
		Slot: domain.ModelSlotSynthesize, Binding: binding, Name: prompts.SynthesizeSystem,
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: system}, {Role: port.ChatRoleUser, Content: user},
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

// Delta recalls what changed in scope since LastSeqSeen and integrates it with MENTAL_MODEL_DELTA.
func (s *singleShot) Delta(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64,
) (deltaDraft, error) {
	units, err := s.recall(ctx, m, cfg, port.SeqUpTo(m.LastSeqSeen, upTo))
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
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, window port.SeqRange,
) ([]domain.Unit, error) {
	scope := resolveScope(m)
	req := recall.RecallRequest{
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

// render renders the SYNTHESIZE system and user messages.
func (s *singleShot) render(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, units []domain.Unit,
) (system, user string, err error) {
	obs, facts := promptFacts(units)
	data := prompts.SynthesizeData{
		Name: m.Spec.Name, SourceQuery: m.Spec.SourceQuery, Mission: cfg.ReflectMission,
		Observations: obs, Facts: facts, MaxTokens: maxContentTokens(m, cfg), Now: s.d.Clock.Now(),
	}
	if s.d.Directives != nil {
		data.Directives, err = s.d.Directives.Select(ctx, m.Bank, m.Spec.Tags, false)
		if err != nil {
			return "", "", fmt.Errorf("mentalmodel: directives: %w", err)
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
