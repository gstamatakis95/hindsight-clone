package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// PromptPreviewer renders the exact messages an operation would send, with no LLM call.
type PromptPreviewer interface {
	// Preview renders op's prompt for bank b with overrides o merged on top of the effective config.
	Preview(
		ctx context.Context, b port.BankRef, op domain.PromptOperation, o domain.BankConfig,
	) (domain.PromptPreview, error)
}
