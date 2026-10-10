package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// entityRepo is port.EntityRepo over one transaction.
type entityRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.EntityRepo = (*entityRepo)(nil)

// Candidates is not implemented yet.
func (r *entityRepo) Candidates(ctx context.Context, b port.BankRef, name string, limit int) ([]domain.EntityCandidate, error) {
	return nil, errNotImplemented
}

// Cooccurrence is not implemented yet.
func (r *entityRepo) Cooccurrence(ctx context.Context, b port.BankRef, entity port.EntityID, others []port.EntityID) (int, error) {
	return 0, errNotImplemented
}

// Upsert is not implemented yet.
func (r *entityRepo) Upsert(ctx context.Context, e domain.Entity) (port.EntityID, error) {
	return "", errNotImplemented
}

// Attach is not implemented yet.
func (r *entityRepo) Attach(ctx context.Context, unit port.UnitID, ents []port.EntityID) error {
	return errNotImplemented
}

// PendingMaintenance is not implemented yet.
func (r *entityRepo) PendingMaintenance(ctx context.Context, b port.BankRef, limit int) ([]port.EntityID, error) {
	return nil, errNotImplemented
}

// Detach is not implemented yet.
func (r *entityRepo) Detach(ctx context.Context, unit port.UnitID) error {
	return errNotImplemented
}

// PruneOrphans is not implemented yet.
func (r *entityRepo) PruneOrphans(ctx context.Context, b port.BankRef, ids []port.EntityID) (int, error) {
	return 0, errNotImplemented
}
