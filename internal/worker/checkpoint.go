package worker

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Checkpoint lets a handler report progress and learn whether it was cancelled.
type Checkpoint interface {
	// Report writes progress via Claimer; returns ErrCancelled once the row is cancelled or the bank is
	// gone (domain.ErrCancelled), so the handler stops at this checkpoint.
	Report(ctx context.Context, p domain.Progress) error
}

// checkpoint implements Checkpoint for one operation.
type checkpoint struct {
	c  store.Claimer
	op domain.OperationID
}

var _ Checkpoint = (*checkpoint)(nil)

// NewCheckpoint returns the Checkpoint of operation op. The caller stamps p.At from its injected Clock.
func NewCheckpoint(c store.Claimer, op domain.OperationID) Checkpoint {
	return &checkpoint{c: c, op: op}
}

// Report stops the handler (domain.ErrCancelled) when its context is done, its row is no longer
// processing (Progress is fenced) or Alive is false (cancel requested, bank tombstoned or gone); otherwise
// it records p. Other Claimer errors are returned unchanged, so the runner retries the job.
func (k *checkpoint) Report(ctx context.Context, p domain.Progress) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrCancelled, err)
	}
	if err := k.c.Progress(ctx, k.op, p); err != nil {
		if errors.Is(err, domain.ErrFailedPrecondition) || errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: operation %s no longer running: %w", domain.ErrCancelled, k.op, err)
		}
		return err
	}
	alive, err := k.c.Alive(ctx, k.op)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("%w: operation %s", domain.ErrCancelled, k.op)
	}
	return nil
}
