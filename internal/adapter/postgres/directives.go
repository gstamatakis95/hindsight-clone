package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// directiveRepo is port.DirectiveRepo over one transaction.
type directiveRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.DirectiveRepo = (*directiveRepo)(nil)

// Upsert is not implemented yet.
func (r *directiveRepo) Upsert(ctx context.Context, d domain.Directive) (port.DirectiveID, error) {
	return "", errNotImplemented
}

// List is not implemented yet.
func (r *directiveRepo) List(ctx context.Context, b port.BankRef, activeOnly bool) ([]domain.Directive, error) {
	return nil, errNotImplemented
}

// Delete is not implemented yet.
func (r *directiveRepo) Delete(ctx context.Context, b port.BankRef, id port.DirectiveID) error {
	return errNotImplemented
}
