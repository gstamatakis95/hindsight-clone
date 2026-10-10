package worker

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
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
// (consolidate) retries forever with capped exponential backoff. An *ErrRetryAt with a non-zero At
// replaces the backoff, but not the cap.
func nextRun(j port.Job, err error, now time.Time) (at time.Time, ok bool) {
	if permanent(err) {
		return time.Time{}, false
	}
	maxAttempts := j.Kind.MaxAttempts()
	if maxAttempts > 0 && j.Attempt+1 >= maxAttempts {
		return time.Time{}, false
	}
	var ra *ErrRetryAt
	if errors.As(err, &ra) && !ra.At.IsZero() {
		return ra.At, true
	}
	if maxAttempts == 0 {
		return now.Add(expBackoff(j.Attempt)), true
	}
	return now.Add(RetryStep * time.Duration(j.Attempt+1)), true
}

// permanent reports whether err must not be retried.
func permanent(err error) bool {
	return errors.Is(err, ErrPermanent) || errors.Is(err, domain.ErrCancelled) ||
		errors.Is(err, domain.ErrInvalidArgument)
}

// expBackoff is RetryStep × 2^attempt, capped at ConsolidateBackoffCap.
func expBackoff(attempt int) time.Duration {
	d := RetryStep
	for i := 0; i < attempt && d < ConsolidateBackoffCap; i++ {
		d *= 2
	}
	return min(d, ConsolidateBackoffCap)
}
