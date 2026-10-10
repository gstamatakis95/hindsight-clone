package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// evidenceRepo is port.EvidenceRepo over one transaction.
type evidenceRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.EvidenceRepo = (*evidenceRepo)(nil)

// Add is not implemented yet.
func (r *evidenceRepo) Add(ctx context.Context, obs port.UnitID, ev []domain.Evidence) error {
	return errNotImplemented
}

// Cites is not implemented yet.
func (r *evidenceRepo) Cites(ctx context.Context, s port.ScopeKey, unit port.UnitID) (bool, error) {
	return false, errNotImplemented
}

// DeleteCiting is not implemented yet.
func (r *evidenceRepo) DeleteCiting(ctx context.Context, b port.BankRef, ids []port.UnitID) (deleted, reset []port.UnitID, err error) {
	return nil, nil, errNotImplemented
}
