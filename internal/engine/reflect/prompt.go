package reflect

import (
	"memd/internal/domain"
	"memd/internal/port"
	"memd/internal/prompts"
	"slices"
	"strings"
	"time"
)

// PromptBuilder renders the reflect system prompt.
type PromptBuilder interface {
	// System renders reflect_mission + disposition rendered as instructions + directives matching the
	// request tags. Takes the effective config, so request overrides reach the prompt. It shows the prompt
	// as a default reflect would send it (every enabled tool offered, no budget section, the clock's now);
	// a render failure returns "".
	System(cfg domain.BankConfig, directives []domain.Directive) string
}

// promptBuilder is the template-backed PromptBuilder.
type promptBuilder struct {
	clock port.Clock
}

var _ PromptBuilder = (*promptBuilder)(nil)

// NewPromptBuilder returns the PromptBuilder that renders the reflect_system template; clock supplies the
// current time shown in the prompt.
func NewPromptBuilder(clock port.Clock) PromptBuilder {
	return &promptBuilder{clock: clock}
}

// System implements PromptBuilder.
func (p *promptBuilder) System(cfg domain.BankConfig, directives []domain.Directive) string {
	data := promptData(cfg, directives, toolSet(cfg, true), p.clock.Now())
	out, err := prompts.Render(prompts.ReflectSystem, &data)
	if err != nil {
		return ""
	}
	return out
}

// promptData fills the ReflectData fields that come from the effective config, the directives and the tools
// offered: mission, disposition, directives and tool flags. The loop adds the query, budget, context, bank
// name and answer target.
func promptData(
	cfg domain.BankConfig, directives []domain.Directive, tools map[string]bool, now time.Time,
) prompts.ReflectData {
	return prompts.ReflectData{
		Mission:      strings.TrimSpace(cfg.ReflectMission),
		Disposition:  cfg.Disposition,
		Directives:   directives,
		MentalModels: tools[ToolSearchMentalModels] || tools[ToolReadMentalModels],
		Observations: tools[ToolSearchObservations],
		Expand:       tools[ToolExpand],
		Now:          now,
	}
}

// retrievalTools lists the retrieval tool names in prompt order.
var retrievalTools = []string{
	ToolSearchMentalModels, ToolReadMentalModels, ToolSearchObservations, ToolRecall, ToolExpand,
}

// toolSet returns the retrieval tools cfg enables (ReflectConfig.EnabledTools; nil = all); pages = false
// drops the page tools. recall is always offered when nothing else is, so the agent can retrieve.
func toolSet(cfg domain.BankConfig, pages bool) map[string]bool {
	var enabled []string
	if cfg.Reflect != nil {
		enabled = cfg.Reflect.EnabledTools
	}
	out := map[string]bool{}
	for _, name := range retrievalTools {
		if enabled != nil && !slices.Contains(enabled, name) {
			continue
		}
		if !pages && (name == ToolSearchMentalModels || name == ToolReadMentalModels) {
			continue
		}
		out[name] = true
	}
	if !out[ToolRecall] && !out[ToolSearchObservations] && !out[ToolSearchMentalModels] {
		out[ToolRecall] = true
	}
	return out
}

// hasRules reports whether any directive has a rule (the predicate reflect_system's DIRECTIVES block uses).
func hasRules(ds []domain.Directive) bool {
	for _, d := range ds {
		if len(d.Rules) > 0 {
			return true
		}
	}
	return false
}
