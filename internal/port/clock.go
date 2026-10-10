package port

import "time"

// Clock is injected; it makes recency and temporal tests deterministic. Logic never calls time.Now.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}
