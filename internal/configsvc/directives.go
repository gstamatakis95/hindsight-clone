package configsvc

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
)

// UpsertDirective creates d (empty ID) or replaces it, and returns it as stored, with normalized tags. It is a
// write: an unknown bank is created with tenant defaults, a tombstoned one is domain.ErrBankDeleting.
func (s *Service) UpsertDirective(ctx context.Context, d domain.Directive) (domain.Directive, error) {
	d.Tags = domain.NormalizeTags(d.Tags)
	var out domain.Directive
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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

// ListDirectives returns the bank's directives sharing a tag with tags (every one when tags is empty), only active
// ones when activeOnly; an unknown or tombstoned bank is domain.ErrNotFound.
func (s *Service) ListDirectives(
	ctx context.Context, b domain.BankRef, tags []string, activeOnly bool,
) ([]domain.Directive, error) {
	dirs, err := s.readDirectives(ctx, b, activeOnly)
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

// DeleteDirective removes one directive. It is a write that never creates the bank: an unknown bank or directive
// is domain.ErrNotFound and a tombstoned bank domain.ErrBankDeleting.
func (s *Service) DeleteDirective(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error {
	return s.d.DB.InTx(ctx, func(tx store.Tx) error {
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.Deleting() {
			return fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
		}
		return tx.Directives().Delete(ctx, b, id)
	})
}

// readDirectives lists bank b's directives in a short transaction (only active ones when activeOnly); an unknown
// or tombstoned bank is domain.ErrNotFound.
func (s *Service) readDirectives(ctx context.Context, b domain.BankRef, activeOnly bool) ([]domain.Directive, error) {
	var out []domain.Directive
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = nil
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.Deleting() {
			return fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
		}
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
