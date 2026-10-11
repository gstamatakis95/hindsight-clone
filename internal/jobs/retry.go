package jobs

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

const (
	// RetryStep is the linear backoff unit: a failed run of attempt a (0-based) retries after
	// RetryStep × (a+1), i.e. 60 s, 120 s, ... until the kind's attempt cap.
	RetryStep = 60 * time.Second
	// ConsolidateBackoffCap caps consolidate's exponential backoff (60 s, 120 s, 240 s, ... ≤ 30 min).
	ConsolidateBackoffCap = 30 * time.Minute
)

// nextRun decides the retry of a run of j that failed with err at now: ok=false means fail for good.
// Permanent errors (ErrPermanent, domain.ErrInvalidArgument) and cancellation never retry. A kind with a
// cap (domain.OperationKind.MaxAttempts > 0) fails for good once attempt+1 reaches it, the same rule
// ReclaimStale applies; below the cap it retries after RetryStep × (attempt+1). An uncapped kind
// (consolidate) retries forever with capped exponential backoff.
func nextRun(j store.Job, err error, now time.Time) (at time.Time, ok bool) {
	if IsPermanent(err) {
		return time.Time{}, false
	}
	maxAttempts := j.Kind.MaxAttempts()
	if maxAttempts > 0 && j.Attempt+1 >= maxAttempts {
		return time.Time{}, false
	}
	if maxAttempts == 0 {
		return now.Add(Backoff(j.Attempt)), true
	}
	return now.Add(RetryStep * time.Duration(j.Attempt+1)), true
}

// IsPermanent reports whether err must not be retried: ErrPermanent, domain.ErrModelUnbound,
// domain.ErrCancelled or domain.ErrInvalidArgument.
func IsPermanent(err error) bool {
	return deterministic(err) || errors.Is(err, domain.ErrCancelled) || errors.Is(err, domain.ErrInvalidArgument)
}

// deterministic reports whether err fails the same way on every run: ErrPermanent, or a model slot with no binding
// (domain.ErrModelUnbound). Such a job fails at once, even when the worker is shutting down.
func deterministic(err error) bool {
	return errors.Is(err, ErrPermanent) || errors.Is(err, domain.ErrModelUnbound)
}

// Backoff is the exponential retry delay after a failed run of attempt (0-based): RetryStep × 2^attempt,
// capped at ConsolidateBackoffCap. Consolidate retries with it, and so does delete_bank's continuation
// after its last attempt.
func Backoff(attempt int) time.Duration {
	d := RetryStep
	for i := 0; i < attempt && d < ConsolidateBackoffCap; i++ {
		d *= 2
	}
	return min(d, ConsolidateBackoffCap)
}

// ErrPermanent marks a deterministic failure (schema violation, integrity error, bad payload): the runner
// fails the job at once with no retry. Wrap it: fmt.Errorf("%w: ...", jobs.ErrPermanent).
var ErrPermanent = errors.New("permanent job failure")
