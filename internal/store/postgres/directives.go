package postgres

import (
	"context"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// directiveRepo is store.DirectiveRepo over one transaction.
type directiveRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.DirectiveRepo = (*directiveRepo)(nil)

// Upsert creates the directive (a new UUID string id when d.ID is empty) or replaces its name, rules, tags
// and active flag, keeping created_at; it returns the id. An unknown bank is domain.ErrNotFound.
func (r *directiveRepo) Upsert(ctx context.Context, d domain.Directive) (domain.DirectiveID, error) {
	if err := checkTags(d.Tags); err != nil {
		return "", err
	}
	id := d.ID
	if id == "" {
		id = domain.DirectiveID(uuid.NewString())
	}
	err := r.q.DirectiveUpsert(ctx, db.DirectiveUpsertParams{
		TenantID: d.Bank.Tenant, BankID: d.Bank.Bank, ID: string(id), Name: d.Name,
		Rules: tagsOrEmpty(d.Rules), Tags: tagsOrEmpty(d.Tags), Active: d.Active,
	})
	if err != nil {
		return "", mapErr(fmt.Sprintf("upsert directive %s", id), err)
	}
	return id, nil
}

// List returns the bank's directives (only active ones when activeOnly), oldest first.
func (r *directiveRepo) List(ctx context.Context, b domain.BankRef, activeOnly bool) ([]domain.Directive, error) {
	rows, err := r.q.DirectiveList(ctx, db.DirectiveListParams{TenantID: b.Tenant, BankID: b.Bank, ActiveOnly: activeOnly})
	if err != nil {
		return nil, mapErr("list directives", err)
	}
	return directivesOf(b, rows), nil
}

// Page returns one keyset page of the bank's directives, oldest first (created_at, then id in byte order). An
// empty q.Tags is no tag filter.
func (r *directiveRepo) Page(ctx context.Context, q store.DirectiveQuery) ([]domain.Directive, error) {
	match, tags := tagFilterArgs(q.Tags, q.TagsMatch)
	arg := db.DirectivePageParams{
		TenantID: q.Bank.Tenant, BankID: q.Bank.Bank, ActiveOnly: q.ActiveOnly, TagsMatch: match, FilterTags: tags,
		Lim: int32(min(q.Limit, math.MaxInt32)), //nolint:gosec // clamped to the int32 range
	}
	if q.After != nil {
		arg.AfterCreatedAt, arg.AfterID = &q.After.CreatedAt, string(q.After.ID)
	}
	rows, err := r.q.DirectivePage(ctx, arg)
	if err != nil {
		return nil, mapErr("page directives", err)
	}
	return directivesOf(q.Bank, rows), nil
}

// directivesOf converts directive rows of bank b.
func directivesOf(b domain.BankRef, rows []db.Directive) []domain.Directive {
	out := make([]domain.Directive, len(rows))
	for i, row := range rows {
		out[i] = domain.Directive{
			ID: domain.DirectiveID(row.ID), Bank: b, Name: row.Name, Rules: row.Rules, Tags: row.Tags,
			Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out
}

// Delete removes the directive; unknown = domain.ErrNotFound.
func (r *directiveRepo) Delete(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error {
	n, err := r.q.DirectiveDelete(ctx, db.DirectiveDeleteParams{TenantID: b.Tenant, BankID: b.Bank, ID: string(id)})
	return docRowsErr(fmt.Sprintf("delete directive %s", id), n, err)
}
