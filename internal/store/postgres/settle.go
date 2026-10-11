package postgres

import (
	"context"
	"memd/internal/store/postgres/db"
)

// SettleRetainBatches ends every PENDING retain_batch parent whose children are all terminal: FAILED when a
// child failed, else CANCELLED when a child was cancelled, else COMPLETED, with per-status counts as its
// result; it returns how many parents it settled. Parents are never claimed, so without it they would stay
// PENDING and block the BankStats barrier. It implements store.Maintenance.
func (s *DB) SettleRetainBatches(ctx context.Context) (int, error) {
	n, err := db.New(s.pool).SettleRetainBatches(ctx)
	if err != nil {
		return 0, mapErr("settle retain batches", err)
	}
	return int(n), nil
}
