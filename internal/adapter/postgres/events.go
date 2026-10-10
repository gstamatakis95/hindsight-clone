package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// eventRepo is port.EventRepo over one transaction.
type eventRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.EventRepo = (*eventRepo)(nil)

// Append is not implemented yet.
func (r *eventRepo) Append(ctx context.Context, e domain.LifecycleEvent) error {
	return errNotImplemented
}

// List is not implemented yet.
func (r *eventRepo) List(ctx context.Context, b port.BankRef, afterSeq int64, limit int, types []domain.EventType) (events []domain.LifecycleEvent, lastSeq int64, err error) {
	return nil, 0, errNotImplemented
}

// Prune is not implemented yet.
func (r *eventRepo) Prune(ctx context.Context, before time.Time) (int, error) {
	return 0, errNotImplemented
}
