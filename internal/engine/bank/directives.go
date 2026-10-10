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

// DirectiveAdmin manages a bank's directives. Its method set is identical to server.DirectiveAdmin, so
// the implementation satisfies the server's interface directly.
type DirectiveAdmin interface {
	// UpsertDirective creates d (empty ID) or replaces it, and returns it as stored. It is a write: an
	// unknown bank is created with tenant defaults, a tombstoned one is domain.ErrBankDeleting.
	UpsertDirective(ctx context.Context, d domain.Directive) (domain.Directive, error)
	// ListDirectives returns the bank's directives whose tags overlap tags (all when tags is empty), only
	// active ones when activeOnly; an unknown or tombstoned bank is domain.ErrNotFound.
	ListDirectives(ctx context.Context, b port.BankRef, tags []string, activeOnly bool) ([]domain.Directive, error)
	// DeleteDirective removes one directive; unknown = domain.ErrNotFound.
	DeleteDirective(ctx context.Context, b port.BankRef, id port.DirectiveID) error
}
