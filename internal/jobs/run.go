package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
)

// Run is one run of a job: the claimed row plus the handle its handler reports through. Report writes
// progress and tells the handler to stop once the run is cancelled or lost; Alive asks the same without
// writing; SetResult records what the runner stores on the operation row when the handler returns nil.
type Run struct {
	// Job is the claimed operations row (for an inline run, the job as the caller built it).
	Job store.Job

	c Claimer // nil for an inline run

	mu     sync.Mutex
	result json.RawMessage
	usage  []domain.Usage
}

// NewRun returns the run of claimed job j, fenced through c. The Runner builds one per claim; tests build
// one over a claimed row to drive a handler directly.
func NewRun(c Claimer, j store.Job) *Run {
	return &Run{Job: j, c: c}
}

// Inline returns a run of j that no worker claimed (a sync retain runs its document in the RPC): Report
// writes nothing and stops nothing, Alive is always true, and the recorded result goes nowhere.
func Inline(j store.Job) *Run {
	return &Run{Job: j}
}

// Report records progress p on the operation row. It returns domain.ErrCancelled, so the handler stops at
// this checkpoint, when ctx is done, the row is no longer processing (Progress is fenced) or Alive is false
// (cancel requested, bank tombstoned or gone). Other Claimer errors are returned unchanged, so the runner
// retries the job. The caller stamps p.At from its injected clock. An inline run records nothing.
func (r *Run) Report(ctx context.Context, p domain.Progress) error {
	if r.c == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrCancelled, err)
	}
	if err := r.c.Progress(ctx, r.Job.ID, p); err != nil {
		if errors.Is(err, domain.ErrFailedPrecondition) || errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: operation %s no longer running: %w", domain.ErrCancelled, r.Job.ID, err)
		}
		return err
	}
	alive, err := r.c.Alive(ctx, r.Job.ID)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("%w: operation %s", domain.ErrCancelled, r.Job.ID)
	}
	return nil
}

// Alive reports whether the run should keep going: its row is PROCESSING without a cancel request and its
// bank is neither tombstoned nor gone (a delete_bank job stays alive). An inline run is always alive.
func (r *Run) Alive(ctx context.Context) (bool, error) {
	if r.c == nil {
		return true, nil
	}
	return r.c.Alive(ctx, r.Job.ID)
}

// SetResult records v, encoded as JSON, as the run's result summary, and appends usage. The runner stores
// both with Claimer.Complete when the handler returns nil; GetOperation shows them, so keep the keys
// stable. A later call replaces the result and appends its usage; a nil v (or one that does not encode)
// keeps the recorded result and only appends usage.
func (r *Run) SetResult(v any, usage ...domain.Usage) {
	var raw json.RawMessage
	if v != nil {
		if b, err := json.Marshal(v); err == nil {
			raw = b
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if raw != nil {
		r.result = raw
	}
	r.usage = append(r.usage, usage...)
}

// Result returns the recorded result summary and usage.
func (r *Run) Result() (json.RawMessage, []domain.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.usage
}
