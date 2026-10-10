package worker

import (
	"context"
	"memd/internal/domain"
)

// Checkpoint lets a handler report progress and learn whether it was cancelled.
type Checkpoint interface {
	// Report writes progress via Claimer; returns ErrCancelled once the row is cancelled or the bank is
	// gone (domain.ErrCancelled), so the handler stops at this checkpoint.
	Report(ctx context.Context, p domain.Progress) error
}
