package worker

import (
	"errors"
	"fmt"
	"time"
)

// ErrPermanent marks a deterministic failure (schema violation, integrity error, bad payload): the runner
// fails the job at once with no retry. Wrap it: fmt.Errorf("%w: ...", worker.ErrPermanent).
var ErrPermanent = errors.New("permanent job failure")

// ErrRetryAt asks the runner to reschedule the job at At instead of the default backoff. It still counts
// as a failed attempt (Claimer.Fail increments the attempt), so the per-kind attempt cap applies.
type ErrRetryAt struct {
	// At is when the job becomes due again.
	At time.Time
	// Cause is the underlying error, recorded on the operation; nil = none.
	Cause error
}

// Error describes the retry and its cause.
func (e *ErrRetryAt) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("retry at %s", e.At.Format(time.RFC3339))
	}
	return fmt.Sprintf("retry at %s: %v", e.At.Format(time.RFC3339), e.Cause)
}

// Unwrap returns the cause.
func (e *ErrRetryAt) Unwrap() error { return e.Cause }

// RetryAt returns an *ErrRetryAt that reschedules the job at at.
func RetryAt(at time.Time, cause error) error {
	return &ErrRetryAt{At: at, Cause: cause}
}
