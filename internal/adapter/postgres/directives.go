package postgres

import (
	"context"
	"fmt"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"

	"github.com/google/uuid"
)

// directiveRepo is port.DirectiveRepo over one transaction.
type directiveRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.DirectiveRepo = (*directiveRepo)(nil)

// Upsert creates the directive (a new UUID string id when d.ID is empty) or replaces its name, rules, tags
// and active flag, keeping created_at; it returns the id. An unknown bank is domain.ErrNotFound.
func (r *directiveRepo) Upsert(ctx context.Context, d domain.Directive) (port.DirectiveID, error) {
	if err := checkTags(d.Tags); err != nil {
		return "", err
	}
	id := d.ID
	if id == "" {
		id = port.DirectiveID(uuid.NewString())
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
func (r *directiveRepo) List(ctx context.Context, b port.BankRef, activeOnly bool) ([]domain.Directive, error) {
	rows, err := r.q.DirectiveList(ctx, db.DirectiveListParams{TenantID: b.Tenant, BankID: b.Bank, ActiveOnly: activeOnly})
	if err != nil {
		return nil, mapErr("list directives", err)
	}
	out := make([]domain.Directive, len(rows))
	for i, row := range rows {
		out[i] = domain.Directive{
			ID: port.DirectiveID(row.ID), Bank: b, Name: row.Name, Rules: row.Rules, Tags: row.Tags,
			Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}

// Delete removes the directive; unknown = domain.ErrNotFound.
func (r *directiveRepo) Delete(ctx context.Context, b port.BankRef, id port.DirectiveID) error {
	n, err := r.q.DirectiveDelete(ctx, db.DirectiveDeleteParams{TenantID: b.Tenant, BankID: b.Bank, ID: string(id)})
	return docRowsErr(fmt.Sprintf("delete directive %s", id), n, err)
}
