package configsvc

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"strings"
)

// CreateDirective adds d to its bank with a new id and returns it as stored, with normalized tags. The name is
// required (empty = domain.ErrInvalidArgument) and must be new in the bank (taken = domain.ErrAlreadyExists). It is
// a write: an unknown bank of an existing org is created, a tombstoned one is domain.ErrBankDeleting.
func (s *Service) CreateDirective(ctx context.Context, d domain.Directive) (domain.Directive, error) {
	if err := checkDirective(d); err != nil {
		return domain.Directive{}, err
	}
	d.ID, d.Tags = "", domain.NormalizeTags(d.Tags)
	var out domain.Directive
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = domain.Directive{}
		if _, err := tx.Banks().Ensure(ctx, d.Bank); err != nil {
			return err
		}
		id, err := tx.Directives().Create(ctx, d)
		if err != nil {
			return err
		}
		out, err = tx.Directives().Get(ctx, d.Bank, id)
		return err
	})
	if err != nil {
		return domain.Directive{}, err
	}
	return out, nil
}

// DirectivePatch computes a directive's new name, rules, tags and active flag from the stored one. It must be pure:
// it runs inside the write transaction and may run again on a retry.
type DirectivePatch func(cur domain.Directive) (domain.Directive, error)

// UpdateDirective patches directive id of bank b and returns it as stored, with normalized tags. It is a write that
// never creates the bank: an unknown bank or directive is domain.ErrNotFound and a tombstoned bank
// domain.ErrBankDeleting. The patched name must stay set (domain.ErrInvalidArgument) and unique in the bank
// (domain.ErrAlreadyExists). The directive row is locked while it is patched, so concurrent patches serialize.
func (s *Service) UpdateDirective(
	ctx context.Context, b domain.BankRef, id domain.DirectiveID, patch DirectivePatch,
) (domain.Directive, error) {
	var out domain.Directive
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = domain.Directive{}
		if err := writableBank(ctx, tx, b); err != nil {
			return err
		}
		cur, err := tx.Directives().Get(ctx, b, id)
		if err != nil {
			return err
		}
		next, err := patch(cur)
		if err != nil {
			return fmt.Errorf("patch directive %s: %w", id, err)
		}
		if err := checkDirective(next); err != nil {
			return err
		}
		next.ID, next.Bank, next.Tags = id, b, domain.NormalizeTags(next.Tags)
		if err := tx.Directives().Update(ctx, next); err != nil {
			return err
		}
		out, err = tx.Directives().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return domain.Directive{}, err
	}
	return out, nil
}

// checkDirective rejects a directive without a name: the name identifies it within the bank.
func checkDirective(d domain.Directive) error {
	if strings.TrimSpace(d.Name) == "" {
		return domain.InvalidField("directive.name", "a directive needs a name")
	}
	return nil
}

// ListDirectivesRequest mirrors memory.v1.ListDirectivesRequest.
type ListDirectivesRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// Tags filters by the directives' tags, flat form only; empty tags = none, unset match = any_strict
	// (directives sharing a tag; untagged ones are left out).
	Tags domain.TagFilter
	// ActiveOnly keeps only active directives.
	ActiveOnly bool
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// DirectivePage is one page of directives.
type DirectivePage struct {
	// Items are the page's directives, oldest first.
	Items []domain.Directive
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListDirectives returns one keyset page of the bank's directives, oldest first (created_at, then id): only active
// ones when req.ActiveOnly, and those whose tags pass the flat tag filter (unset match = any_strict). The page size
// follows domain.ListPage; tag groups and a malformed page token are domain.ErrInvalidArgument. An unknown or
// tombstoned bank is domain.ErrNotFound.
func (s *Service) ListDirectives(ctx context.Context, req ListDirectivesRequest) (DirectivePage, error) {
	q, err := directiveQuery(req)
	if err != nil {
		return DirectivePage{}, err
	}
	size := q.Limit
	q.Limit = size + 1
	var dirs []domain.Directive
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		dirs = nil
		if err := liveBank(ctx, tx, req.Bank); err != nil {
			return err
		}
		var err error
		dirs, err = tx.Directives().Page(ctx, q)
		return err
	})
	if err != nil {
		return DirectivePage{}, err
	}
	page := DirectivePage{Items: dirs}
	if len(dirs) > size {
		page.Items = dirs[:size]
		last := page.Items[size-1]
		page.NextPageToken, err = domain.EncodePageToken(store.DirectiveCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return DirectivePage{}, fmt.Errorf("list directives: %w", err)
		}
	}
	return page, nil
}

// directiveQuery turns a request into a DirectiveQuery, validating the page size and the flat tag filter and
// decoding the page token.
func directiveQuery(req ListDirectivesRequest) (store.DirectiveQuery, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return store.DirectiveQuery{}, fmt.Errorf("list directives: %w", err)
	}
	if err := req.Tags.RequireFlat(); err != nil {
		return store.DirectiveQuery{}, fmt.Errorf("list directives: %w", err)
	}
	match := req.Tags.Match
	if match == domain.TagsMatchUnspecified {
		match = domain.TagsMatchAnyStrict
	}
	q := store.DirectiveQuery{
		Bank: req.Bank, ActiveOnly: req.ActiveOnly, Tags: domain.NormalizeTags(req.Tags.Tags), TagsMatch: match,
		Limit: size,
	}
	if req.PageToken != "" {
		var c store.DirectiveCursor
		if err := domain.DecodePageToken(req.PageToken, &c); err != nil {
			return store.DirectiveQuery{}, fmt.Errorf("list directives: %w", err)
		}
		q.After = &c
	}
	return q, nil
}

// DeleteDirective removes one directive. It is a write that never creates the bank: an unknown bank or directive
// is domain.ErrNotFound and a tombstoned bank domain.ErrBankDeleting.
func (s *Service) DeleteDirective(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error {
	return s.d.DB.InTx(ctx, func(tx store.Tx) error {
		if err := writableBank(ctx, tx, b); err != nil {
			return err
		}
		return tx.Directives().Delete(ctx, b, id)
	})
}

// writableBank returns nil when b exists and is not tombstoned: unknown is domain.ErrNotFound and tombstoned
// domain.ErrBankDeleting. Directive writes other than a create use it, so they never create the bank.
func writableBank(ctx context.Context, tx store.Tx, b domain.BankRef) error {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return err
	}
	if bank.Deleting() {
		return fmt.Errorf("%w: bank %s", domain.ErrBankDeleting, b)
	}
	return nil
}

// readDirectives lists bank b's directives in a short transaction (only active ones when activeOnly); an unknown
// or tombstoned bank is domain.ErrNotFound.
func (s *Service) readDirectives(ctx context.Context, b domain.BankRef, activeOnly bool) ([]domain.Directive, error) {
	var out []domain.Directive
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = nil
		if err := liveBank(ctx, tx, b); err != nil {
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

// liveBank returns domain.ErrBankNotFound unless b exists and is not tombstoned.
func liveBank(ctx context.Context, tx store.Tx, b domain.BankRef) error {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return err
	}
	if bank.Deleting() {
		return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b)
	}
	return nil
}
