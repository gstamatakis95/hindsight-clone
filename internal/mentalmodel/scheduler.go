package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/recall"
	"memd/internal/store"
	"slices"
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

// needsRefresh is the staleness check: stale if an in-scope unit or observation has seq > m.LastSeqSeen, or a
// source was retracted. It reads BankSeq first and returns it as upTo, the snapshot bound for a refresh. Fuzzy
// scope tags resolve at cfg's recall.fuzzy_tag_min_similarity, the floor recall uses, so staleness sees exactly
// the units a refresh would retrieve. Untagged units never stale a tagged model.
func (s *Service) needsRefresh(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig,
) (stale bool, upTo int64, err error) {
	read := s.d.DB.Read()
	upTo, err = read.BankSeq(ctx, m.Bank)
	if err != nil {
		return false, 0, fmt.Errorf("mentalmodel: bank seq: %w", err)
	}
	q, err := scopeQuery(ctx, s.d.Recall, m, domain.SeqUpTo(m.LastSeqSeen, upTo), true,
		recall.FuzzyTagMinSimilarity(cfg))
	if err != nil {
		return false, 0, err
	}
	newest, err := read.NewestSeqInScope(ctx, q)
	if err != nil {
		return false, 0, fmt.Errorf("mentalmodel: scope seq: %w", err)
	}
	if isStale(m, newest, false) {
		return true, upTo, nil
	}
	retracted, err := retractedSources(ctx, read, m)
	if err != nil {
		return false, 0, err
	}
	return isStale(m, newest, retracted), upTo, nil
}

// modelConfig is m's effective config without call overrides: server < tenant < bank < the model's overrides.
// Staleness, scheduling and reads use it.
func (s *Service) modelConfig(ctx context.Context, m domain.MentalModel) (domain.BankConfig, error) {
	cfg, err := s.d.Config.Effective(ctx, m.Bank, configLevels(m, nil)...)
	if err != nil {
		return domain.BankConfig{}, fmt.Errorf("mentalmodel: config: %w", err)
	}
	return cfg, nil
}

// isStale is the staleness rule: something in scope newer than the watermark, or a retracted source.
func isStale(m domain.MentalModel, newestInScopeSeq int64, retracted bool) bool {
	return retracted || newestInScopeSeq > m.LastSeqSeen
}

// retractedSources reports whether any of m's source_ids is no longer a live unit (invalidated, or
// replaced by a reprocess or upsert).
func retractedSources(ctx context.Context, r store.Reader, m domain.MentalModel) (bool, error) {
	ids := slices.Clone(m.SourceIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return false, nil
	}
	live, err := r.GetUnits(ctx, m.Bank, ids)
	if err != nil {
		return false, fmt.Errorf("mentalmodel: read sources: %w", err)
	}
	return len(live) < len(ids), nil
}
