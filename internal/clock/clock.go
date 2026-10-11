package clock

import "time"

// Clock is injected wherever logic needs the time; it makes recency, debounce and temporal tests deterministic.
// Logic never calls time.Now.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// System is the production Clock: the wall clock.
type System struct{}

var _ Clock = System{}

// Now returns the wall-clock time.
func (System) Now() time.Time { return time.Now() }
