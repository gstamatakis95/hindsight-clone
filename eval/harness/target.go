package harness

import (
	"context"
	"time"
)

// Target is the memory service under evaluation. Every method is scoped to one bank; a bank is created
// by its first Retain.
type Target interface {
	// Name labels the target in the report, for example memd or hindsight.
	Name() string
	// Retain stores one session synchronously: it returns once the session's memories are committed. It
	// returns any token usage the service reports for the call (memd's sync Retain reports none).
	Retain(ctx context.Context, bank string, s Session) ([]Usage, error)
	// PendingOperations returns the bank's pending and processing background operations, counted by kind.
	// An empty map means the bank is idle (the eval barrier).
	PendingOperations(ctx context.Context, bank string) (map[string]int64, error)
	// Recall returns the texts of the memories recalled for query, best first.
	Recall(ctx context.Context, bank, query string, at time.Time) ([]string, error)
	// Reflect returns the reflect agent's answer to query and the token usage it reports.
	Reflect(ctx context.Context, bank, query string, at time.Time) (string, []Usage, error)
	// Background returns the token usage of the bank's background operations and how many of them failed.
	Background(ctx context.Context, bank string) (BackgroundStats, error)
}

// BackgroundStats summarizes a bank's background operations after its questions were asked.
type BackgroundStats struct {
	// Usage is the summed token usage of every operation the target reports.
	Usage []Usage
	// Failed counts operations that ended FAILED.
	Failed int
}
