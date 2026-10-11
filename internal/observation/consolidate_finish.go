package observation

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// finish runs the final transaction: stamps the planned units that had no scope left to decide (still
// unchanged and still cited), checks whether unconsolidated units remain and if so enqueues the
// continuation, and appends CONSOLIDATION_COMPLETED.
func (r *run) finish(ctx context.Context) error {
	var want []domain.UnitID
	for _, id := range r.done {
		if !r.parked[id] && !r.unsettled[id] {
			want = append(want, id)
		}
	}
	var more bool
	var stamped []domain.UnitID
	err := r.c.d.DB.InTx(ctx, func(tx store.Tx) error {
		now := r.c.d.Clock.Now()
		seq, err := tx.Seq(ctx, r.job.Bank)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		live, err := liveFacts(ctx, tx, r.job.Bank, want, r.units)
		if err != nil {
			return err
		}
		if stamped, err = stampable(ctx, tx, want, live, r.cited); err != nil {
			return err
		}
		if len(stamped) > 0 {
			if err := tx.Units().MarkConsolidated(ctx, stamped, now); err != nil {
				return fmt.Errorf("stamp: %w", err)
			}
		}
		left, err := tx.Units().Unconsolidated(ctx, r.job.Bank, r.filter, 1)
		if err != nil {
			return fmt.Errorf("unconsolidated units: %w", err)
		}
		if more = len(left) > 0; more {
			if _, _, err := jobs.EnqueueScopedConsolidate(ctx, tx, r.job.Bank, r.payload.Scopes, r.job.Overrides); err != nil {
				return fmt.Errorf("enqueue continuation: %w", err)
			}
		}
		return tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeConsolidationCompleted, Bank: r.job.Bank, At: now, OperationID: r.job.ID,
		})
	})
	if err != nil {
		return fmt.Errorf("consolidate: finish: %w", err)
	}
	r.processed += len(stamped)
	r.more = more
	return nil
}

// refreshCandidates lists, once per run and outside any transaction, the bank's models with
// refresh_after_consolidation that are not paused (Reader.ListMentalModels, every page).
func (r *run) refreshCandidates(ctx context.Context) ([]domain.MentalModel, error) {
	all, _, err := r.c.d.DB.Read().ListMentalModels(ctx, store.ModelQuery{
		Bank: r.job.Bank, RefreshAfterConsolidation: true,
	})
	if err != nil {
		return nil, fmt.Errorf("consolidate: list mental models: %w", err)
	}
	var out []domain.MentalModel
	for _, m := range all {
		if m.Spec.Trigger.RefreshAfterConsolidation && !m.AutoRefreshPaused {
			out = append(out, m)
		}
	}
	return out, nil
}

// reaching returns the run's refresh candidates that scope s reaches.
func (r *run) reaching(s domain.ScopeKey) []domain.MentalModel {
	var out []domain.MentalModel
	for _, m := range r.models {
		if modelReaches(m, s.Tags) {
			out = append(out, m)
		}
	}
	return out
}

// modelReaches reports whether observations of a scope with tags fall in model m's scope: its tag
// groups (AND-ed) when set, else its tags under its tags_match (unset = all_strict when tagged, any when
// untagged). The shared scope never reaches a tagged model (untagged writes never make it stale).
func modelReaches(m domain.MentalModel, tags []string) bool {
	tr := m.Spec.Trigger
	tagged := len(domain.NormalizeTags(m.Spec.Tags)) > 0 || len(tr.TagGroups) > 0
	if tagged && len(tags) == 0 {
		return false
	}
	if len(tr.TagGroups) > 0 {
		for _, g := range tr.TagGroups {
			if !domain.MatchGroup(tags, g) {
				return false
			}
		}
		return true
	}
	mode := tr.TagsMatch
	if mode == domain.TagsMatchUnspecified {
		mode = domain.TagsMatchAny
		if tagged {
			mode = domain.TagsMatchAllStrict
		}
	}
	return domain.MatchTags(tags, m.Spec.Tags, mode)
}

// enqueueRefreshes queues one refresh per model in the caller's tx (which already took tx.Seq), due at
// domain.RefreshDueAt: after refresh_debounce_seconds or at the end of the model's interval floor, whichever
// is later. A model with a queued refresh joins it (refresh_mental_model always dedupes as pending).
func enqueueRefreshes(
	ctx context.Context, tx store.Tx, b domain.BankRef, mm *domain.MentalModelConfig, models []domain.MentalModel,
	now time.Time,
) error {
	mc := refreshConfig(mm)
	for _, m := range models {
		runAt, _ := domain.RefreshDueAt(m, mc, now)
		req := jobs.RefreshRequest{Bank: b, ID: m.Spec.ID, RunAt: runAt}
		if _, _, err := jobs.EnqueueRefresh(ctx, tx, req, now); err != nil {
			return fmt.Errorf("enqueue refresh of %s: %w", m.Spec.ID, err)
		}
	}
	return nil
}

// refreshConfig is mm as consolidation applies it to refresh triggers: an unset or non-positive
// refresh_debounce_seconds is the default debounce (domain.DefaultRefreshDebounceSeconds).
func refreshConfig(mm *domain.MentalModelConfig) *domain.MentalModelConfig {
	if mm == nil {
		return nil
	}
	out := *mm
	if out.RefreshDebounceSeconds != nil && *out.RefreshDebounceSeconds <= 0 {
		out.RefreshDebounceSeconds = nil
	}
	return &out
}
