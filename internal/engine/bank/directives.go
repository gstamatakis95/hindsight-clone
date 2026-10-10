package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// DirectiveSelector returns active directives matching the request tags (untagged = global).
type DirectiveSelector interface {
	// Select returns bank b's active directives that are global or match tags; all = every active
	// directive regardless of tags (apply_all_directives).
	Select(ctx context.Context, b port.BankRef, tags []string, all bool) ([]domain.Directive, error)
}
