package clock

import (
	"sync"
	"time"
)

// DefaultStart is the time a Manual clock built with a zero start begins at: 2025-01-01T00:00:00Z.
// Treat it as a constant; tests compare against it.
var DefaultStart = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

// Manual is a [Clock] for tests. Time only moves when the test calls Set or Advance; Now never reads the
// wall clock. It is safe for concurrent use.
type Manual struct {
	mu  sync.Mutex
	now time.Time
}

var _ Clock = (*Manual)(nil)

// NewManual returns a Manual clock reading start; a zero start means DefaultStart.
func NewManual(start time.Time) *Manual {
	if start.IsZero() {
		start = DefaultStart
	}
	return &Manual{now: start}
}

// Now returns the clock's current time.
func (c *Manual) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t (moving it backwards is allowed).
func (c *Manual) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d (a negative d moves it back) and returns the new time.
func (c *Manual) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}
