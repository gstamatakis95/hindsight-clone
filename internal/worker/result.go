package worker

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"sync"
)

// resultKey is the context key of a job's result recorder.
type resultKey struct{}

// recorder holds what a handler reported for its operation row.
type recorder struct {
	mu     sync.Mutex
	result json.RawMessage
	usage  []domain.Usage
}

// ReportResult records the job's result summary and per-slot usage; the runner stores them with
// Claimer.Complete when the handler returns nil. A later call replaces the result and appends usage. It is
// a no-op outside a job's context (for example in a handler unit test that calls it directly).
func ReportResult(ctx context.Context, result json.RawMessage, usage ...domain.Usage) {
	r, ok := ctx.Value(resultKey{}).(*recorder)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if result != nil {
		r.result = result
	}
	r.usage = append(r.usage, usage...)
}

// withRecorder returns ctx carrying a fresh recorder.
func withRecorder(ctx context.Context) (context.Context, *recorder) {
	r := &recorder{}
	return context.WithValue(ctx, resultKey{}, r), r
}

// get returns the recorded result and usage.
func (r *recorder) get() (json.RawMessage, []domain.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.usage
}
