package worker

import (
	"context"
	"fmt"
)

// BatchSettler ends retain_batch parents once their children are terminal (SPEC "Operations": the parent's
// status aggregates its children). A parent is never claimed or run, so this tick step is what moves it out
// of PENDING; the Postgres adapter implements it (decision 1453).
type BatchSettler interface {
	// SettleRetainBatches ends every PENDING retain_batch parent whose children are all terminal (FAILED if
	// one failed, else CANCELLED if one was cancelled, else COMPLETED) and returns how many it settled. It
	// is idempotent under concurrent callers.
	SettleRetainBatches(ctx context.Context) (int, error)
}

// settleBatches runs the BatchSettler step.
func settleBatches(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Batches == nil {
		return 0, nil
	}
	n, err := d.Batches.SettleRetainBatches(ctx)
	if err != nil {
		return n, fmt.Errorf("settle retain batches: %w", err)
	}
	return n, nil
}
