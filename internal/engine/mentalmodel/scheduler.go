package mentalmodel

import (
	"context"
	"memd/internal/domain"
	"time"
)

// RefreshScheduler decides whether an automatic trigger runs now, parks, or is skipped.
//
// A refresh_mental_model operation that ends FAILED (handler Fail(nil) or reclaim past max attempts) is
// turned into auto_refresh_paused plus a REFRESH_FAILED history entry by an idempotent reconcile in
// RunMaintenance, which finds models whose latest refresh operation failed and that are not yet paused
// or recorded (decision 012, R10'). The Claimer never touches mental-model tables.
type RefreshScheduler interface {
	// Decide applies auto_refresh_paused, the trigger kind, debounce and the interval floor to m at now.
	Decide(ctx context.Context, m domain.MentalModel, now time.Time) (RefreshDecision, error)
}

// DecisionKind is what a RefreshScheduler decided.
type DecisionKind string

// Decision kinds.
const (
	// DecisionUnspecified is the zero value.
	DecisionUnspecified DecisionKind = ""
	// DecisionRunNow enqueues the refresh due now.
	DecisionRunNow DecisionKind = "run_now"
	// DecisionPark enqueues the refresh with run_at = RefreshDecision.Until (debounce or interval floor).
	DecisionPark DecisionKind = "park"
	// DecisionSkip enqueues nothing (paused, not stale, or the trigger doesn't apply).
	DecisionSkip DecisionKind = "skip"
)

// RefreshDecision is a RefreshScheduler's verdict for one model.
type RefreshDecision struct {
	// Kind is run now, park or skip.
	Kind DecisionKind
	// Until is when a parked refresh becomes due (DecisionPark only).
	Until time.Time
	// Reason explains a park or skip, for logs and traces.
	Reason string
}
