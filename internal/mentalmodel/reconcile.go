package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"strings"
)

// Failure reasons recorded on REFRESH_FAILED history entries, derived from the failed operation's error.
const (
	// failureDeltaOpsAllSkipped: every delta op was rejected, even after the retry.
	failureDeltaOpsAllSkipped = "delta_ops_all_skipped"
	// failureEmptyCandidate: the synthesizer returned an empty document.
	failureEmptyCandidate = "empty_candidate"
	// failureStructuredOutput: the structured-output projection failed.
	failureStructuredOutput = "structured_output_failed"
	// failureContentTooLarge: the drafted page passed domain.MaxMentalModelContentBytes.
	failureContentTooLarge = "content_too_large"
	// failureRetrieval: a retrieval or model call failed (reflect, recall, synthesize, delta).
	failureRetrieval = "retrieval_failed"
	// failureUnexpected: anything else.
	failureUnexpected = "unexpected_error"
)

// ReconcileFailedRefreshes is the maintenance tick's failed-refresh sweep. It looks, per model, at the latest
// terminal refresh operation (store.Maintenance.FailedRefreshes). When that failed after the model's last
// completed refresh and is not recorded yet, it sets auto_refresh_paused and last_refresh_failed_at (= the
// failure time, which is the idempotence key) and appends one REFRESH_FAILED history entry and event, each model
// in its own short transaction. It returns how many models it paused.
func (s *Service) ReconcileFailedRefreshes(ctx context.Context) (int, error) {
	if s.d.Maintenance == nil {
		return 0, nil
	}
	failed, err := s.d.Maintenance.FailedRefreshes(ctx)
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: failed refreshes: %w", err)
	}
	n := 0
	var errs []error
	for _, f := range failed {
		paused, err := s.record(ctx, f)
		if err != nil && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrBankDeleting) {
			errs = append(errs, fmt.Errorf("mentalmodel: reconcile %s: %w", f.ModelID, err))
		}
		if paused {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// record pauses f's model and writes the failure, unless it is stale (see skipFailure). A cheap check
// first skips without taking the bank lock (so a paused model doesn't bump the seq every tick); then
// tx.Seq locks the bank row, which serializes this with every other model-row write, and the check is
// repeated on a fresh read.
func (s *Service) record(ctx context.Context, f store.FailedRefresh) (bool, error) {
	keep := defaultHistoryMaxEntries
	if cfg, err := s.d.Config.Effective(ctx, f.Bank); err == nil {
		keep = historyKeep(cfg)
	}
	paused := false
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		paused = false
		m, err := tx.MentalModels().Get(ctx, f.Bank, f.ModelID)
		if err != nil || skipFailure(m, f) {
			return err
		}
		seq, err := tx.Seq(ctx, f.Bank)
		if err != nil {
			return err
		}
		if m, err = tx.MentalModels().Get(ctx, f.Bank, f.ModelID); err != nil || skipFailure(m, f) {
			return err
		}
		at := f.FailedAt
		m.AutoRefreshPaused, m.LastRefreshFailedAt = true, &at
		e := domain.MentalModelHistoryEntry{
			Kind: domain.HistoryKindRefreshFailed, CreatedAt: f.FailedAt, Outcome: domain.RefreshOutcomeFailed,
			FailureReason: failureReason(f.Error), ErrorMessage: f.Error,
		}
		if err := tx.MentalModels().AppendHistory(ctx, f.Bank, f.ModelID, e, keep); err != nil {
			return err
		}
		if err := tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeMentalModelRefreshFailed, Bank: f.Bank, CreatedAt: s.d.Clock.Now(),
			OperationID: f.OperationID, SubjectID: string(f.ModelID),
		}); err != nil {
			return err
		}
		paused = true
		return tx.MentalModels().Update(ctx, m)
	})
	return paused, err
}

// skipFailure reports whether f is not to be recorded on m: it predates the model (a model recreated with
// the same id), a later refresh succeeded, or it is already recorded.
func skipFailure(m domain.MentalModel, f store.FailedRefresh) bool {
	return f.FailedAt.Before(m.CreatedAt) ||
		(m.LastRefreshedAt != nil && !m.LastRefreshedAt.Before(f.FailedAt)) ||
		(m.LastRefreshFailedAt != nil && !m.LastRefreshFailedAt.Before(f.FailedAt))
}

// failureReason classifies a failed refresh's error text.
func failureReason(msg string) string {
	switch {
	case strings.Contains(msg, errAllOpsRejected.Error()):
		return failureDeltaOpsAllSkipped
	case strings.Contains(msg, failureEmptyCandidate):
		return failureEmptyCandidate
	case strings.Contains(msg, failureStructuredOutput):
		return failureStructuredOutput
	case strings.Contains(msg, failureContentTooLarge):
		return failureContentTooLarge
	case strings.Contains(msg, domain.ErrUnavailable.Error()):
		return failureRetrieval
	}
	return failureUnexpected
}

// RunRefreshJob runs one refresh_mental_model job. It first reports the run's start (stage = the job kind, 0 of
// 1 done): a cancelled job, or one whose bank is being deleted, stops there (domain.ErrCancelled) before any
// model call. It then decodes the payload, refreshes with the payload's flags, the job's overrides and its
// operation id, and records the outcome, version, effective mode and strategy, facts used and usage as the
// result. A model (or bank) deleted before the job ran is done, not failed, so a job that runs twice or after a
// delete is harmless. A malformed payload is domain.ErrInvalidArgument.
func (s *Service) RunRefreshJob(ctx context.Context, run *jobs.Run) error {
	p := domain.Progress{Stage: string(run.Job.Kind), RecordedAt: s.d.Clock.Now(), Total: 1}
	if err := run.Report(ctx, p); err != nil {
		return fmt.Errorf("%s: checkpoint: %w", run.Job.Kind, err)
	}
	out, err := s.runJob(ctx, run.Job)
	if err != nil {
		return fmt.Errorf("refresh_mental_model: %w", err)
	}
	run.SetResult(map[string]any{
		"outcome": out.Trace.Outcome, "version": out.VersionWritten, "effective_mode": out.Trace.EffectiveMode,
		"effective_strategy": out.Trace.EffectiveStrategy, "facts_used": out.FactsUsed,
	}, out.Trace.Usage...)
	return nil
}

// runJob decodes j's payload and runs the refresh it asks for. A model or bank that is gone (ErrNotFound) is
// done, not failed.
func (s *Service) runJob(ctx context.Context, j store.Job) (RefreshOutcome, error) {
	p, err := decodeRefreshPayload(j.Payload)
	if err != nil {
		return RefreshOutcome{}, err
	}
	out, err := s.refresh(ctx, j.Bank, p.MentalModelID, refreshOptions{
		Manual: p.Manual, ForceFull: p.ForceFull, Strategy: p.Strategy, Overrides: j.Overrides,
		OperationID: j.ID,
	})
	if errors.Is(err, domain.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("mentalmodel: refresh %s: %w", p.MentalModelID, err)
	}
	return out, nil
}

// decodeRefreshPayload reads a refresh_mental_model payload; one without a mental_model_id is
// domain.ErrInvalidArgument.
func decodeRefreshPayload(raw []byte) (jobs.RefreshPayload, error) {
	var p jobs.RefreshPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.MentalModelID == "" {
		return jobs.RefreshPayload{}, fmt.Errorf("%w: refresh_mental_model payload %s", domain.ErrInvalidArgument, raw)
	}
	return p, nil
}
