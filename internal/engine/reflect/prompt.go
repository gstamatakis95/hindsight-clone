package reflect

import "memd/internal/domain"

// PromptBuilder renders the reflect system prompt.
type PromptBuilder interface {
	// System renders reflect_mission + disposition rendered as instructions + directives matching the
	// request tags. Takes the effective config, so request overrides reach the prompt.
	System(cfg domain.BankConfig, directives []domain.Directive) string
}
