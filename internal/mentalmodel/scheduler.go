package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"time"
)

// decisionKind is what decide decided.
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
	// reasonPaused: auto_refresh_paused is set; only a manual refresh runs.
	reasonPaused = "auto_refresh_paused"
	// reasonNotStale: nothing in scope changed and no source was retracted.
	reasonNotStale = "not_stale"
)

// refreshDecision is decide's verdict for one model.
type refreshDecision struct {
	// Kind is run now, park or skip.
	Kind decisionKind
	// Until is when a parked refresh becomes due (decisionPark only).
	Until time.Time
	// Reason explains a park or skip, for logs and traces.
	Reason string
}

// decide applies auto_refresh_paused, staleness, debounce and the interval floor to an automatic refresh of
// m at now. It skips a paused or fresh model; otherwise the refresh is due at the later of now + debounce and
// last_refreshed_at + the interval floor. Call it outside any transaction (it reads config and the store).
func (s *Service) decide(ctx context.Context, m domain.MentalModel, now time.Time) (refreshDecision, error) {
	if m.AutoRefreshPaused {
		return refreshDecision{Kind: decisionSkip, Reason: reasonPaused}, nil
	}
	cfg, err := s.modelConfig(ctx, m)
	if err != nil {
		return refreshDecision{}, err
	}
	stale, _, err := s.needsRefresh(ctx, m, cfg)
	if err != nil {
		return refreshDecision{}, fmt.Errorf("mentalmodel: staleness of %s: %w", m.Spec.ID, err)
	}
	if !stale {
		return refreshDecision{Kind: decisionSkip, Reason: reasonNotStale}, nil
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
