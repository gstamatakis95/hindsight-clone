package consolidate

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Refresh-trigger defaults.
const (
	// defaultDebounceSeconds is refresh_debounce_seconds when unset (the spec's 30).
	defaultDebounceSeconds = 30
	// refreshSerialPrefix prefixes a model id into its refresh serial key, mm:{model_id}.
	refreshSerialPrefix = "mm:"
)

// RefreshPayload is the payload of the refresh_mental_model jobs consolidation triggers: the model only,
// no flags (decision 1004).
type RefreshPayload struct {
	// ModelID is the mental model to refresh.
	ModelID port.MentalModelID `json:"model_id"`
}

// finish runs the final transaction: stamps the planned units that had no scope left to decide, checks
// whether unconsolidated units remain and if so enqueues the continuation (DedupePending), enqueues
// refreshes of the models the changed scopes reach, and appends CONSOLIDATION_COMPLETED.
func (r *run) finish(ctx context.Context) error {
	models, err := r.refreshCandidates(ctx)
	if err != nil {
		return err
	}
	var stamp []port.UnitID
	for _, id := range r.done {
		if !r.parked[id] {
			stamp = append(stamp, id)
		}
	}
	var more bool
	err = r.c.d.Store.InTx(ctx, func(tx port.Tx) error {
		now := r.c.d.Clock.Now()
		seq, err := tx.Seq(ctx, r.job.Bank)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		if len(stamp) > 0 {
			if err := tx.Units().MarkConsolidated(ctx, stamp, now); err != nil {
				return fmt.Errorf("stamp: %w", err)
			}
		}
		left, err := tx.Units().Unconsolidated(ctx, r.job.Bank, r.filter, 1)
		if err != nil {
			return fmt.Errorf("unconsolidated units: %w", err)
		}
		if more = len(left) > 0; more {
			if _, _, err := tx.Queue().Enqueue(ctx, r.continuation()); err != nil {
				return fmt.Errorf("enqueue continuation: %w", err)
			}
		}
		if err := r.enqueueRefreshes(ctx, tx, models, now); err != nil {
			return err
		}
		return tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeConsolidationCompleted, Bank: r.job.Bank, At: now, OperationID: r.job.ID,
		})
	})
	if err != nil {
		return fmt.Errorf("consolidate: finish: %w", err)
	}
	r.processed += len(stamp)
	r.more = more
	return nil
}

// continuation is the next run of this job: same scopes and overrides, DedupePending (a self-continuation
// joins a queued consolidate; one with overrides never joins, as Queue documents).
func (r *run) continuation() port.Job {
	payload := r.job.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return port.Job{
		Bank: r.job.Bank, Kind: domain.OperationKindConsolidate, SerialKey: SerialKey,
		Payload: payload, Overrides: r.job.Overrides, Dedupe: port.DedupePending,
	}
}

// refreshCandidates lists the bank's models with refresh_after_consolidation, not paused, whose scope a
// changed scope reaches. It reads outside the final transaction.
func (r *run) refreshCandidates(ctx context.Context) ([]domain.MentalModel, error) {
	if r.c.d.Models == nil || len(r.changed) == 0 {
		return nil, nil
	}
	all, err := r.c.d.Models.MentalModels(ctx, r.job.Bank)
	if err != nil {
		return nil, fmt.Errorf("consolidate: list mental models: %w", err)
	}
	var out []domain.MentalModel
	for _, m := range all {
		if !m.Spec.Trigger.RefreshAfterConsolidation || m.AutoRefreshPaused {
			continue
		}
		for _, s := range r.changed {
			if modelReaches(m, s.Tags) {
				out = append(out, m)
				break
			}
		}
	}
	return out, nil
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

// enqueueRefreshes queues one refresh per model, due after refresh_debounce_seconds or at the end of
// the model's interval floor, whichever is later; a model with a queued refresh joins it.
func (r *run) enqueueRefreshes(ctx context.Context, tx port.Tx, models []domain.MentalModel, now time.Time) error {
	mm := r.cfg.MentalModels
	if mm == nil {
		mm = &domain.MentalModelConfig{}
	}
	debounce := time.Duration(orDefault(mm.RefreshDebounceSeconds, defaultDebounceSeconds)) * time.Second
	for _, m := range models {
		runAt := now.Add(debounce)
		floor := mm.MinRefreshIntervalSeconds
		if m.Spec.Trigger.MinRefreshIntervalSeconds != nil {
			floor = m.Spec.Trigger.MinRefreshIntervalSeconds
		}
		if floor != nil && *floor > 0 && m.LastRefreshedAt != nil {
			if end := m.LastRefreshedAt.Add(time.Duration(*floor) * time.Second); end.After(runAt) {
				runAt = end
			}
		}
		payload, err := json.Marshal(RefreshPayload{ModelID: m.Spec.ID})
		if err != nil {
			return fmt.Errorf("refresh payload: %w", err)
		}
		_, _, err = tx.Queue().Enqueue(ctx, port.Job{
			Bank: r.job.Bank, Kind: domain.OperationKindRefreshMentalModel,
			SerialKey: refreshSerialPrefix + string(m.Spec.ID), Payload: payload, RunAt: runAt,
			Dedupe: port.DedupePending,
		})
		if err != nil {
			return fmt.Errorf("enqueue refresh of %s: %w", m.Spec.ID, err)
		}
	}
	return nil
}
