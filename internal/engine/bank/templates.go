package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// TemplateCodec exports a bank's config, directives and model specs, and creates banks from them.
type TemplateCodec interface {
	// Export returns bank b's own config, directives and mental-model specs (no memories).
	Export(ctx context.Context, b port.BankRef) (domain.BankTemplate, error)
	// Create makes bank b from t in one transaction, reassigning directive ids and enqueueing a first
	// build of every mental model; an existing bank is domain.ErrAlreadyExists.
	Create(ctx context.Context, b port.BankRef, t domain.BankTemplate) (domain.Bank, error)
}
