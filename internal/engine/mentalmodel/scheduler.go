package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"time"
)

// RefreshScheduler decides whether an automatic trigger runs now, parks, or is skipped.
//
// A refresh_mental_model operation that ends FAILED (handler Fail(nil) or reclaim past max attempts) is
// turned into auto_refresh_paused plus a REFRESH_FAILED history entry by an idempotent reconcile in
// the maintenance tick (RefreshFailureReconciler), which finds models whose latest terminal refresh operation
// failed and that are not yet paused or recorded (decision 012, R10'). The Claimer never touches
// mental-model tables.
type RefreshScheduler interface {
	// Decide applies auto_refresh_paused, staleness, debounce and the interval floor to m at now.
	Decide(ctx context.Context, m domain.MentalModel, now time.Time) (refreshDecision, error)
}

// decisionKind is what a RefreshScheduler decided.
type decisionKind string

// Decision kinds.
const (
	// decisionRunNow enqueues the refresh due now.
	decisionRunNow decisionKind = "run_now"
	// decisionPark enqueues the refresh with run_at = refreshDecision.Until (debounce or interval floor).
	decisionPark decisionKind = "park"
	// decisionSkip enqueues nothing (paused, not stale, or the trigger doesn't apply).
	decisionSkip decisionKind = "skip"
)

// Skip reasons; a park's reason is domain.RefreshReasonDebounce or domain.RefreshReasonIntervalFloor.
const (
	// ReasonPaused: auto_refresh_paused is set; only a manual refresh runs.
	ReasonPaused = "auto_refresh_paused"
	// ReasonNotStale: nothing in scope changed and no source was retracted.
	ReasonNotStale = "not_stale"
)

// refreshDecision is a RefreshScheduler's verdict for one model.
type refreshDecision struct {
	// Kind is run now, park or skip.
	Kind decisionKind
	// Until is when a parked refresh becomes due (decisionPark only).
	Until time.Time
	// Reason explains a park or skip, for logs and traces.
	Reason string
}

// scheduler is the RefreshScheduler.
type scheduler struct {
	detect ChangeDetector
	config config.Resolver
}

var _ RefreshScheduler = (*scheduler)(nil)

// NewRefreshScheduler returns the RefreshScheduler. Call Decide outside any transaction (it reads config
// and the change detector).
func NewRefreshScheduler(d ChangeDetector, c config.Resolver) RefreshScheduler {
	return &scheduler{detect: d, config: c}
}

// Decide skips a paused or fresh model; otherwise the refresh is due at the later of now + debounce and
// last_refreshed_at + the interval floor.
func (s *scheduler) Decide(ctx context.Context, m domain.MentalModel, now time.Time) (refreshDecision, error) {
	if m.AutoRefreshPaused {
		return refreshDecision{Kind: decisionSkip, Reason: ReasonPaused}, nil
	}
	stale, _, err := s.detect.NeedsRefresh(ctx, m)
	if err != nil {
		return refreshDecision{}, fmt.Errorf("mentalmodel: staleness of %s: %w", m.Spec.ID, err)
	}
	if !stale {
		return refreshDecision{Kind: decisionSkip, Reason: ReasonNotStale}, nil
	}
	cfg, err := s.config.Effective(ctx, m.Bank, configLevels(m, nil)...)
	if err != nil {
		return refreshDecision{}, fmt.Errorf("mentalmodel: config: %w", err)
	}
	until, reason := domain.RefreshDueAt(m, cfg.MentalModels, now)
	if !until.After(now) {
		return refreshDecision{Kind: decisionRunNow}, nil
	}
	return refreshDecision{Kind: decisionPark, Until: until, Reason: reason}, nil
}

// RefreshRequest asks for one refresh of a model (the RefreshMentalModel RPC).
type RefreshRequest struct {
	// Bank owns the model.
	Bank domain.BankRef
	// ID is the model refreshed.
	ID domain.MentalModelID
	// Manual marks a user request: it ignores the interval floor, releases a parked refresh and, on
	// success, clears auto_refresh_paused.
	Manual bool
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool
	// Overrides are per-call overrides; nil = none (a joined refresh keeps its own).
	Overrides *domain.BankConfig
	// Strategy overrides the refresh strategy for this call; unspecified = the model's.
	Strategy domain.RefreshStrategy
	// RunAt is when the refresh becomes due; zero = now. Ignored when joining a queued refresh.
	RunAt time.Time
}

// job is the request as the job catalogue takes it.
func (r RefreshRequest) job() jobs.RefreshRequest {
	return jobs.RefreshRequest{
		Bank: r.Bank, ID: r.ID, Manual: r.Manual, ForceFull: r.ForceFull, Overrides: r.Overrides, Strategy: r.Strategy,
		RunAt: r.RunAt,
	}
}
