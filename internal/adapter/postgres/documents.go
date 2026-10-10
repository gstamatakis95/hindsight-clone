package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// documentRepo is port.DocumentRepo over one transaction.
type documentRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.DocumentRepo = (*documentRepo)(nil)

// Get is not implemented yet.
func (r *documentRepo) Get(ctx context.Context, b port.BankRef, id port.DocumentID) (domain.Document, error) {
	return domain.Document{}, errNotImplemented
}

// Upsert is not implemented yet.
func (r *documentRepo) Upsert(ctx context.Context, d domain.Document) error {
	return errNotImplemented
}

// Complete is not implemented yet.
func (r *documentRepo) Complete(ctx context.Context, b port.BankRef, id port.DocumentID, contentHash, overridesHash string) error {
	return errNotImplemented
}

// SetTags is not implemented yet.
func (r *documentRepo) SetTags(ctx context.Context, b port.BankRef, id port.DocumentID, tags []string) error {
	return errNotImplemented
}
