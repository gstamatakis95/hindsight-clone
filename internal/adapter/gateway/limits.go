package gateway

import (
	"context"
	"fmt"
	"memd/internal/domain"
)

// limiter caps in-flight gateway calls: one process-wide semaphore plus an optional semaphore per model slot.
// Each permit covers one HTTP attempt; retries wait for their backoff without holding a permit.
type limiter struct {
	// global bounds every call the client makes.
	global chan struct{}
	// slots bounds the calls of one slot; a slot without an entry is limited by global only.
	slots map[domain.ModelSlot]chan struct{}
}

// newLimiter builds a limiter allowing global calls at once, and slots[s] calls of slot s (entries <= 0 are
// ignored).
func newLimiter(global int, slots map[domain.ModelSlot]int) *limiter {
	l := &limiter{global: make(chan struct{}, global), slots: map[domain.ModelSlot]chan struct{}{}}
	for s, n := range slots {
		if n > 0 {
			l.slots[s] = make(chan struct{}, n)
		}
	}
	return l
}

// acquire takes the slot's permit (if the slot is limited), then a global one, so a saturated slot never
// holds global capacity while it waits. It returns the function that releases both, or ctx's error.
func (l *limiter) acquire(ctx context.Context, slot domain.ModelSlot) (func(), error) {
	slotSem := l.slots[slot]
	if slotSem != nil {
		if err := take(ctx, slotSem); err != nil {
			return nil, err
		}
	}
	if err := take(ctx, l.global); err != nil {
		if slotSem != nil {
			<-slotSem
		}
		return nil, err
	}
	return func() {
		<-l.global
		if slotSem != nil {
			<-slotSem
		}
	}, nil
}

// take puts one token into sem, waiting until there is room or ctx is done.
func take(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for a gateway permit: %w", ctx.Err())
	}
}
