package worker

import (
	"context"
	"fmt"
)

// settleBatches runs the retain_batch settle step.
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
