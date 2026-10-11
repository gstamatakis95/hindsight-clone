package bank

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
)

// DirectiveSelector returns active directives matching the request tags (untagged = global).
type DirectiveSelector interface {
	// Select returns bank b's active directives that are global or match tags; all = every active
	// directive regardless of tags (apply_all_directives).
	Select(ctx context.Context, b domain.BankRef, tags []string, all bool) ([]domain.Directive, error)
}

// DirectiveAdmin manages a bank's directives. Its method set is identical to server.DirectiveAdmin, so
// the implementation satisfies the server's interface directly.
type DirectiveAdmin interface {
	// UpsertDirective creates d (empty ID) or replaces it, and returns it as stored. It is a write: an
	// unknown bank is created with tenant defaults, a tombstoned one is domain.ErrBankDeleting.
	UpsertDirective(ctx context.Context, d domain.Directive) (domain.Directive, error)
	// ListDirectives returns the bank's directives whose tags overlap tags (all when tags is empty), only
	// active ones when activeOnly; an unknown or tombstoned bank is domain.ErrNotFound.
	ListDirectives(ctx context.Context, b domain.BankRef, tags []string, activeOnly bool) ([]domain.Directive, error)
	// DeleteDirective removes one directive; unknown = domain.ErrNotFound.
	DeleteDirective(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error
}

// readDirectives lists bank b's directives in a short transaction (only active ones when activeOnly); an
// unknown or tombstoned bank is domain.ErrNotFound.
func readDirectives(ctx context.Context, s store.DB, b domain.BankRef, activeOnly bool) ([]domain.Directive, error) {
	var out []domain.Directive
	err := s.InTx(ctx, func(tx store.Tx) error {
		out = nil
		if _, err := liveBank(ctx, tx, b); err != nil {
			return err
		}
		var err error
		out, err = tx.Directives().List(ctx, b, activeOnly)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// overlaps reports whether the normalized tag sets a and b share a tag.
func overlaps(a, b []string) bool {
	for _, t := range a {
		if slices.Contains(b, t) {
			return true
		}
	}
	return false
}

// directiveSelector implements DirectiveSelector over store.DB.
type directiveSelector struct{ s store.DB }

var _ DirectiveSelector = (*directiveSelector)(nil)

// NewDirectiveSelector returns the DirectiveSelector over s. Directives are read in a short transaction
// (store.Reader has no directive read; decision 1306).
func NewDirectiveSelector(s store.DB) DirectiveSelector { return &directiveSelector{s: s} }

// Select keeps the active directives that are untagged (global) or share a tag with the request; with no
// request tags only global ones apply (Hindsight's isolation mode), and all keeps every active one.
func (d *directiveSelector) Select(
	ctx context.Context, b domain.BankRef, tags []string, all bool,
) ([]domain.Directive, error) {
	active, err := readDirectives(ctx, d.s, b, true)
	if err != nil {
		return nil, err
	}
	want := domain.NormalizeTags(tags)
	out := make([]domain.Directive, 0, len(active))
	for _, dir := range active {
		own := domain.NormalizeTags(dir.Tags)
		if all || len(own) == 0 || overlaps(own, want) {
			out = append(out, dir)
		}
	}
	return out, nil
}

// directiveAdmin implements DirectiveAdmin over store.DB.
type directiveAdmin struct{ s store.DB }

var _ DirectiveAdmin = (*directiveAdmin)(nil)

// NewDirectiveAdmin returns the DirectiveAdmin over s.
func NewDirectiveAdmin(s store.DB) DirectiveAdmin { return &directiveAdmin{s: s} }

// UpsertDirective ensures the bank, normalizes the tags and stores d, returning the stored directive.
func (a *directiveAdmin) UpsertDirective(ctx context.Context, d domain.Directive) (domain.Directive, error) {
	d.Tags = domain.NormalizeTags(d.Tags)
	var out domain.Directive
	err := a.s.InTx(ctx, func(tx store.Tx) error {
		out = domain.Directive{}
		if _, err := tx.Banks().Ensure(ctx, d.Bank); err != nil {
			return err
		}
		id, err := tx.Directives().Upsert(ctx, d)
		if err != nil {
			return err
		}
		all, err := tx.Directives().List(ctx, d.Bank, false)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(all, func(x domain.Directive) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("%w: directive %s vanished after upsert", domain.ErrNotFound, id)
		}
		out = all[i]
		return nil
	})
	if err != nil {
		return domain.Directive{}, err
	}
	return out, nil
}

// ListDirectives returns the bank's directives sharing a tag with tags (every one when tags is empty).
func (a *directiveAdmin) ListDirectives(
	ctx context.Context, b domain.BankRef, tags []string, activeOnly bool,
) ([]domain.Directive, error) {
	dirs, err := readDirectives(ctx, a.s, b, activeOnly)
	if err != nil {
		return nil, err
	}
	want := domain.NormalizeTags(tags)
	if len(want) == 0 {
		return dirs, nil
	}
	out := make([]domain.Directive, 0, len(dirs))
	for _, dir := range dirs {
		if overlaps(domain.NormalizeTags(dir.Tags), want) {
			out = append(out, dir)
		}
	}
	return out, nil
}

// DeleteDirective removes one directive. It is a write: an unknown bank or directive is
// domain.ErrNotFound and a tombstoned bank domain.ErrBankDeleting.
func (a *directiveAdmin) DeleteDirective(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error {
	return a.s.InTx(ctx, func(tx store.Tx) error {
		if _, err := writableBank(ctx, tx, b); err != nil {
			return err
		}
		return tx.Directives().Delete(ctx, b, id)
	})
}
