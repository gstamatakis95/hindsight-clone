package mentalmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// RefreshScheduler decides whether an automatic trigger runs now, parks, or is skipped.
//
// A refresh_mental_model operation that ends FAILED (handler Fail(nil) or reclaim past max attempts) is
// turned into auto_refresh_paused plus a REFRESH_FAILED history entry by an idempotent reconcile in
// RunMaintenance (RefreshFailureReconciler), which finds models whose latest terminal refresh operation
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

// Skip and park reasons.
const (
	// ReasonPaused: auto_refresh_paused is set; only a manual refresh runs.
	ReasonPaused = "auto_refresh_paused"
	// ReasonNotStale: nothing in scope changed and no source was retracted.
	ReasonNotStale = "not_stale"
	// ReasonDebounce: parked for refresh_debounce_seconds so a burst joins one refresh.
	ReasonDebounce = "debounce"
	// ReasonIntervalFloor: parked until min_refresh_interval_seconds after the last refresh.
	ReasonIntervalFloor = "min_refresh_interval"
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
	config port.ConfigResolver
}

var _ RefreshScheduler = (*scheduler)(nil)

// NewRefreshScheduler returns the RefreshScheduler. Call Decide outside any transaction (it reads config
// and the change detector).
func NewRefreshScheduler(d ChangeDetector, c port.ConfigResolver) RefreshScheduler {
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
	until, reason := dueAt(m, cfg, now)
	if !until.After(now) {
		return refreshDecision{Kind: decisionRunNow}, nil
	}
	return refreshDecision{Kind: decisionPark, Until: until, Reason: reason}, nil
}

// dueAt is when an automatic refresh of m may run: now + refresh_debounce_seconds, or the end of the
// interval floor when that is later.
func dueAt(m domain.MentalModel, cfg domain.BankConfig, now time.Time) (time.Time, string) {
	debounce, floor := defaultDebounceSeconds, 0
	if mc := cfg.MentalModels; mc != nil {
		if mc.RefreshDebounceSeconds != nil {
			debounce = *mc.RefreshDebounceSeconds
		}
		if mc.MinRefreshIntervalSeconds != nil {
			floor = *mc.MinRefreshIntervalSeconds
		}
	}
	if m.Spec.Trigger.MinRefreshIntervalSeconds != nil {
		floor = *m.Spec.Trigger.MinRefreshIntervalSeconds
	}
	at, reason := now.Add(time.Duration(max(debounce, 0))*time.Second), ReasonDebounce
	if floor > 0 && m.LastRefreshedAt != nil {
		if end := m.LastRefreshedAt.Add(time.Duration(floor) * time.Second); end.After(at) {
			at, reason = end, ReasonIntervalFloor
		}
	}
	return at, reason
}

// RefreshPayload is the refresh_mental_model job payload. Optional keys are omitted when unset, so
// Queue.Upgrade keeps a queued row's value unless a joiner sets it (force_full is OR-ed).
type RefreshPayload struct {
	// MentalModelID is the model refreshed.
	MentalModelID port.MentalModelID `json:"mental_model_id"`
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool `json:"force_full,omitempty"`
	// Strategy overrides the trigger's strategy for this refresh.
	Strategy domain.RefreshStrategy `json:"strategy,omitempty"`
	// Manual marks a user-requested refresh; its success clears auto_refresh_paused.
	Manual bool `json:"manual,omitempty"`
}

// RefreshRequest asks for one refresh of a model.
type RefreshRequest struct {
	// Bank owns the model.
	Bank port.BankRef
	// ID is the model refreshed.
	ID port.MentalModelID
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

// SerialKey is the refresh_mental_model serial key of model id: refreshes of one model never overlap.
func SerialKey(id port.MentalModelID) string { return "mm:" + string(id) }

// EnqueueRefresh queues req's refresh in tx, or joins the model's queued (PENDING) refresh and upgrades
// it: force_full OR-ed, strategy and overrides replaced when given, manual set. A manual request also
// releases a parked refresh (run_at = now). joined reports a join. The caller calls tx.Seq(req.Bank)
// first (the Seq-before-Enqueue lock order).
func EnqueueRefresh(
	ctx context.Context, tx port.Tx, req RefreshRequest, now time.Time,
) (id port.OperationID, joined bool, err error) {
	payload, err := json.Marshal(RefreshPayload{
		MentalModelID: req.ID, ForceFull: req.ForceFull, Strategy: req.Strategy, Manual: req.Manual,
	})
	if err != nil {
		return "", false, fmt.Errorf("mentalmodel: refresh payload: %w", err)
	}
	runAt := req.RunAt
	if req.Manual || runAt.IsZero() {
		runAt = now
	}
	id, joined, err = tx.Queue().Enqueue(ctx, port.Job{
		Bank: req.Bank, Kind: domain.OperationKindRefreshMentalModel, SerialKey: SerialKey(req.ID),
		Payload: payload, Overrides: req.Overrides, RunAt: runAt, Dedupe: port.DedupePending,
	})
	if err != nil || !joined {
		return id, joined, err
	}
	if req.ForceFull || req.Manual || req.Strategy != domain.RefreshStrategyUnspecified || req.Overrides != nil {
		if err := tx.Queue().Upgrade(ctx, id, payload, req.Overrides); err != nil {
			return "", false, err
		}
	}
	if req.Manual {
		if err := tx.Queue().Reschedule(ctx, id, now); err != nil {
			return "", false, err
		}
	}
	return id, true, nil
}
