package harness

import (
	"context"
	"time"
)

// Clock is the runner's time source, injected so tests control latency and polling.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep waits for d or until ctx is done, returning ctx's error in the latter case.
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the wall clock.
type SystemClock struct{}

var _ Clock = SystemClock{}

// Now returns time.Now.
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits for d or until ctx is done.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}
