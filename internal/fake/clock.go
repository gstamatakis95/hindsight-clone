package fake

import (
	"memd/internal/port"
	"sync"
	"time"
)

// DefaultClockStart is the time a Clock built with a zero start begins at: 2025-01-01T00:00:00Z.
// Treat it as a constant; tests compare against it.
var DefaultClockStart = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

// Clock is a controllable port.Clock. Time only moves when the test calls Set or Advance; Now never
// reads the wall clock. It is safe for concurrent use.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

var _ port.Clock = (*Clock)(nil)

// NewClock returns a Clock reading start; a zero start means DefaultClockStart.
func NewClock(start time.Time) *Clock {
	if start.IsZero() {
		start = DefaultClockStart
	}
	return &Clock{now: start}
}

// Now returns the clock's current time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t (moving it backwards is allowed).
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d (a negative d moves it back) and returns the new time.
func (c *Clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}
