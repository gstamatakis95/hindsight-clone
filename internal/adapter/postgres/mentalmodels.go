package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// mentalModelRepo is port.MentalModelRepo over one transaction.
type mentalModelRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.MentalModelRepo = (*mentalModelRepo)(nil)

// Get is not implemented yet.
func (r *mentalModelRepo) Get(ctx context.Context, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error) {
	return domain.MentalModel{}, errNotImplemented
}

// Create is not implemented yet.
func (r *mentalModelRepo) Create(ctx context.Context, m domain.MentalModel) error {
	return errNotImplemented
}

// Update is not implemented yet.
func (r *mentalModelRepo) Update(ctx context.Context, m domain.MentalModel) error {
	return errNotImplemented
}

// Delete is not implemented yet.
func (r *mentalModelRepo) Delete(ctx context.Context, b port.BankRef, id port.MentalModelID) error {
	return errNotImplemented
}

// AppendHistory is not implemented yet.
func (r *mentalModelRepo) AppendHistory(ctx context.Context, b port.BankRef, id port.MentalModelID, e domain.MentalModelHistoryEntry, keep int) error {
	return errNotImplemented
}

// History is not implemented yet.
func (r *mentalModelRepo) History(ctx context.Context, b port.BankRef, id port.MentalModelID, kind domain.HistoryKind, limit int) ([]domain.MentalModelHistoryEntry, error) {
	return nil, errNotImplemented
}
