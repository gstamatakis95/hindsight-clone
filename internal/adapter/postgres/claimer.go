package postgres

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Claim is not implemented yet.
func (r *Store) Claim(ctx context.Context, worker string, kinds []port.JobKind, n int) ([]port.Job, error) {
	return nil, errNotImplemented
}

// Heartbeat is not implemented yet.
func (r *Store) Heartbeat(ctx context.Context, worker string, ids []port.OperationID) error {
	return errNotImplemented
}

// Progress is not implemented yet.
func (r *Store) Progress(ctx context.Context, id port.OperationID, p domain.Progress) error {
	return errNotImplemented
}

// Alive is not implemented yet.
func (r *Store) Alive(ctx context.Context, id port.OperationID) (bool, error) {
	return false, errNotImplemented
}

// Complete is not implemented yet.
func (r *Store) Complete(ctx context.Context, id port.OperationID, result json.RawMessage, usage []port.Usage) error {
	return errNotImplemented
}

// Fail is not implemented yet.
func (r *Store) Fail(ctx context.Context, id port.OperationID, cause error, retryAt *time.Time) error {
	return errNotImplemented
}

// ReclaimStale is not implemented yet.
func (r *Store) ReclaimStale(ctx context.Context, ttl time.Duration) (int, error) {
	return 0, errNotImplemented
}
