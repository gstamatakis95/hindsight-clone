package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
)

// Failure reasons recorded on REFRESH_FAILED history entries, derived from the failed operation's error.
const (
	// FailureDeltaOpsAllSkipped: every delta op was rejected, even after the retry.
	FailureDeltaOpsAllSkipped = "delta_ops_all_skipped"
	// FailureEmptyCandidate: the synthesizer returned an empty document.
	FailureEmptyCandidate = "empty_candidate"
	// FailureStructuredOutput: the structured-output projection failed.
	FailureStructuredOutput = "structured_output_failed"
	// FailureRetrieval: a retrieval or model call failed (reflect, recall, synthesize, delta).
	FailureRetrieval = "retrieval_failed"
	// FailureUnexpected: anything else.
	FailureUnexpected = "unexpected_error"
)

// RefreshFailureReconciler turns failed refreshes into paused models; it implements
// worker.RefreshFailureReconciler.
type RefreshFailureReconciler interface {
	// ReconcileFailedRefreshes looks, per model, at the latest terminal refresh operation. When it failed
	// after the model's last completed refresh and is not recorded yet, it sets auto_refresh_paused and
	// last_refresh_failed_at (= the failure time, which is the idempotence key) and appends one
	// REFRESH_FAILED history entry and event. It returns how many models it paused.
	ReconcileFailedRefreshes(ctx context.Context) (int, error)
}

// reconciler is the RefreshFailureReconciler.
type reconciler struct {
	src    FailureSource
	store  port.Store
	config port.ConfigResolver
	clock  port.Clock
}

var _ RefreshFailureReconciler = (*reconciler)(nil)

// NewRefreshFailureReconciler returns the reconciler over the failed-refresh read (port.Reader.FailedRefreshes).
func NewRefreshFailureReconciler(
	src FailureSource, s port.Store, c port.ConfigResolver, clock port.Clock,
) RefreshFailureReconciler {
	return &reconciler{src: src, store: s, config: c, clock: clock}
}

// ReconcileFailedRefreshes records every unrecorded failure, each in its own short transaction.
func (r *reconciler) ReconcileFailedRefreshes(ctx context.Context) (int, error) {
	failed, err := r.src.FailedRefreshes(ctx)
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: failed refreshes: %w", err)
	}
	n := 0
	var errs []error
	for _, f := range failed {
		paused, err := r.record(ctx, f)
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
func (r *reconciler) record(ctx context.Context, f FailedRefresh) (bool, error) {
	keep := defaultHistoryMaxEntries
	if cfg, err := r.config.Effective(ctx, f.Bank); err == nil {
		keep = historyKeep(cfg)
	}
	paused := false
	err := r.store.InTx(ctx, func(tx port.Tx) error {
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
			Kind: domain.HistoryKindRefreshFailed, ChangedAt: f.FailedAt, Outcome: domain.RefreshOutcomeFailed,
			FailureReason: failureReason(f.Error), ErrorMessage: f.Error,
		}
		if err := tx.MentalModels().AppendHistory(ctx, f.Bank, f.ModelID, e, keep); err != nil {
			return err
		}
		if err := tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeMentalModelRefreshFailed, Bank: f.Bank, At: r.clock.Now(),
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
func skipFailure(m domain.MentalModel, f FailedRefresh) bool {
	return f.FailedAt.Before(m.CreatedAt) ||
		(m.LastRefreshedAt != nil && !m.LastRefreshedAt.Before(f.FailedAt)) ||
		(m.LastRefreshFailedAt != nil && !m.LastRefreshFailedAt.Before(f.FailedAt))
}

// failureReason classifies a failed refresh's error text.
func failureReason(msg string) string {
	switch {
	case strings.Contains(msg, ErrAllOpsRejected.Error()):
		return FailureDeltaOpsAllSkipped
	case strings.Contains(msg, FailureEmptyCandidate):
		return FailureEmptyCandidate
	case strings.Contains(msg, FailureStructuredOutput):
		return FailureStructuredOutput
	case strings.Contains(msg, domain.ErrUnavailable.Error()):
		return FailureRetrieval
	}
	return FailureUnexpected
}

// RunJob runs one refresh_mental_model job through r: it decodes the payload and passes the job's
// overrides and operation id. A model (or bank) deleted before the job ran is done, not failed, so a job
// that runs twice or after a delete is harmless. A malformed payload is domain.ErrInvalidArgument.
func RunJob(ctx context.Context, r RefreshRunner, j port.Job) (RefreshOutcome, error) {
	p, err := decodeRefreshPayload(j.Payload)
	if err != nil {
		return RefreshOutcome{}, err
	}
	out, err := r.Run(ctx, j.Bank, p.MentalModelID, RefreshOptions{
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

// decodeRefreshPayload reads a refresh_mental_model payload. Rows queued by consolidation before decision
// 1800 carry the model under "model_id"; that key is read when "mental_model_id" is missing.
func decodeRefreshPayload(raw []byte) (RefreshPayload, error) {
	var p struct {
		RefreshPayload
		// LegacyModelID is the pre-1800 consolidation key.
		LegacyModelID port.MentalModelID `json:"model_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return RefreshPayload{}, fmt.Errorf("%w: refresh_mental_model payload %s", domain.ErrInvalidArgument, raw)
	}
	if p.MentalModelID == "" {
		p.MentalModelID = p.LegacyModelID
	}
	if p.MentalModelID == "" {
		return RefreshPayload{}, fmt.Errorf("%w: refresh_mental_model payload %s", domain.ErrInvalidArgument, raw)
	}
	return p.RefreshPayload, nil
}
