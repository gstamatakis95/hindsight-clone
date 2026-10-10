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
	data := promptData(cfg, directives, toolSet(cfg, true, nil), p.clock.Now())
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
		Mission:            strings.TrimSpace(cfg.ReflectMission),
		Disposition:        cfg.Disposition,
		Directives:         directives,
		SearchMentalModels: tools[ToolSearchMentalModels],
		ReadMentalModels:   tools[ToolReadMentalModels],
		Observations:       tools[ToolSearchObservations],
		Expand:             tools[ToolExpand],
		Recall:             tools[ToolRecall],
		Now:                now,
	}
}

// retrievalTools lists the retrieval tool names in prompt order.
var retrievalTools = []string{
	ToolSearchMentalModels, ToolReadMentalModels, ToolSearchObservations, ToolRecall, ToolExpand,
}

// toolSet returns the retrieval tools cfg enables (ReflectConfig.EnabledTools; nil = all) for a request
// limited to types (empty = all): pages = false drops the page tools, search_observations needs observations
// among the types and recall needs a raw-fact type.
func toolSet(cfg domain.BankConfig, pages bool, types []domain.FactType) map[string]bool {
	var enabled []string
	if cfg.Reflect != nil {
		enabled = cfg.Reflect.EnabledTools
	}
	off := map[string]bool{
		ToolSearchMentalModels: !pages,
		ToolReadMentalModels:   !pages,
		ToolSearchObservations: len(types) > 0 && !slices.Contains(types, domain.FactTypeObservation),
		ToolRecall:             len(types) > 0 && len(factTypes(types)) == 0,
	}
	out := map[string]bool{}
	for _, name := range retrievalTools {
		if !off[name] && (enabled == nil || slices.Contains(enabled, name)) {
			out[name] = true
		}
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
