package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
)

// SettleRetainBatches ends every PENDING retain_batch parent whose children are all terminal: FAILED when a
// child failed, else CANCELLED when a child was cancelled, else COMPLETED, with per-status counts as its
// result; it returns how many parents it settled. Parents are never claimed, so without it they would stay
// PENDING and block the BankStats barrier. It is not a port method: the worker's maintenance tick consumes it
// through worker.BatchSettler, narrowed in cmd/memd/wire.go (decision 1453).
func (s *Store) SettleRetainBatches(ctx context.Context) (int, error) {
	n, err := db.New(s.pool).SettleRetainBatches(ctx)
	if err != nil {
		return 0, mapErr("settle retain batches", err)
	}
	return int(n), nil
}
