package postgres

import (
	"context"
	"encoding/json"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// queue is port.Queue over one transaction.
type queue struct {
	q  *db.Queries
	tx *tx
}

var _ port.Queue = (*queue)(nil)

// Enqueue is not implemented yet.
func (r *queue) Enqueue(ctx context.Context, j port.Job) (id port.OperationID, joined bool, err error) {
	return "", false, errNotImplemented
}

// Upgrade is not implemented yet.
func (r *queue) Upgrade(ctx context.Context, id port.OperationID, payload json.RawMessage, o *domain.BankConfig) error {
	return errNotImplemented
}

// Reschedule is not implemented yet.
func (r *queue) Reschedule(ctx context.Context, id port.OperationID, at time.Time) error {
	return errNotImplemented
}

// CancelBank is not implemented yet.
func (r *queue) CancelBank(ctx context.Context, b port.BankRef) (int, error) {
	return 0, errNotImplemented
}

// Get is not implemented yet.
func (r *queue) Get(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	return domain.Operation{}, errNotImplemented
}

// List is not implemented yet.
func (r *queue) List(ctx context.Context, b port.BankRef, f port.OperationFilter) ([]domain.Operation, string, error) {
	return nil, "", errNotImplemented
}

// Cancel is not implemented yet.
func (r *queue) Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	return domain.Operation{}, errNotImplemented
}

// Retry is not implemented yet.
func (r *queue) Retry(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	return domain.Operation{}, errNotImplemented
}

// Prune is not implemented yet.
func (r *queue) Prune(ctx context.Context, before time.Time) (int, error) {
	return 0, errNotImplemented
}
