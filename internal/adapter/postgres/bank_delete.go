package postgres

import (
	"context"
	"memd/internal/port"
)

// DeleteBatch is not implemented yet.
func (r *bankRepo) DeleteBatch(ctx context.Context, b port.BankRef, limit int) (int, error) {
	return 0, errNotImplemented
}

// Delete is not implemented yet.
func (r *bankRepo) Delete(ctx context.Context, b port.BankRef) error {
	return errNotImplemented
}
