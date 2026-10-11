package consolidate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"time"
)

// applyInput is one batch's decision to write.
type applyInput struct {
	scope   domain.ScopeKey
	actions []ConsolidationAction
	// incomplete reports that decide dropped part of the decision (mergeUpdates): nothing is stamped.
	incomplete bool
	batch      []domain.UnitID               // the batch's facts
	stamp      []domain.UnitID               // the facts whose last scope this is: stamped in this transaction
	expect     map[domain.UnitID]domain.Unit // the facts as planned (seq and tags)
	cited      map[domain.UnitID][]domain.ScopeKey
	refresh    []domain.MentalModel // models this scope reaches: refreshed when the batch writes
	mm         *domain.MentalModelConfig
}

// applyOutput is what one apply transaction committed.
type applyOutput struct {
	wrote     bool            // an observation was written
	cited     []domain.UnitID // facts the written observations cite in this scope
	stamped   []domain.UnitID // facts stamped consolidated
	unsettled []domain.UnitID // facts whose decision did not fully land: not stamped this run
}

// apply writes one batch's decision in one short transaction with no model call: tx.Seq, then each
// action (observation row stamped with the seq, evidence with exact quotes, an observation_versions row),
// the refreshes of the models the scope reaches, then consolidated_at on the facts this scope finishes.
//
// A fact edited, retagged or removed since it was planned (different seq or tags, or gone) is not live.
// An action citing a fact that is not live, or updating an observation that is gone or left the scope, is
// skipped whole (never rewritten or trimmed), and then no fact of the batch is stamped: the next run
// re-decides them (decision 1000).
func (c *consolidator) apply(ctx context.Context, in applyInput) (applyOutput, error) {
	var out applyOutput
	err := c.d.Store.InTx(ctx, func(tx store.Tx) error {
		out = applyOutput{}
		now := c.d.Clock.Now()
		// Seq first, every time: it locks the bank row, so no edit or retag can commit between the
		// re-read below and the stamp (writers of the bank serialize on that row).
		seq, err := tx.Seq(ctx, in.scope.BankRef)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		live, err := liveFacts(ctx, tx, in.scope.BankRef, in.batch, in.expect)
		if err != nil {
			return err
		}
		var complete bool
		if out.cited, complete, err = c.writeActions(ctx, tx, in, live, seq, now); err != nil {
			return err
		}
		out.wrote = len(out.cited) > 0
		complete = complete && !in.incomplete
		if out.wrote {
			if err := enqueueRefreshes(ctx, tx, in.scope.BankRef, in.mm, in.refresh, now); err != nil {
				return err
			}
		}
		for _, id := range in.batch {
			if _, ok := live[id]; !complete || !ok {
				out.unsettled = append(out.unsettled, id)
			}
		}
		if !complete {
			return nil
		}
		if out.stamped, err = stampable(ctx, tx, in.stamp, live, in.cited); err != nil {
			return err
		}
		if len(out.stamped) == 0 {
			return nil
		}
		return tx.Units().MarkConsolidated(ctx, out.stamped, now)
	})
	if err != nil {
		return applyOutput{}, fmt.Errorf("consolidate: apply in scope %q: %w", in.scope.String(), err)
	}
	return out, nil
}

// writeActions writes every action that can land, stamped with seq, and returns the facts the written
// actions cite (empty when nothing was written) and whether every action landed.
func (c *consolidator) writeActions(
	ctx context.Context, tx store.Tx, in applyInput, live map[domain.UnitID]domain.Unit, seq int64, now time.Time,
) (cited []domain.UnitID, complete bool, err error) {
	complete = true
	for _, a := range in.actions {
		obs, ok, err := c.target(ctx, tx, in.scope, a, live)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			complete = false
			continue
		}
		if err := writeObservation(ctx, tx, obs, a, seq, now); err != nil {
			return nil, false, err
		}
		for _, e := range a.Evidence {
			if !slices.Contains(cited, e.Unit) {
				cited = append(cited, e.Unit)
			}
		}
	}
	return cited, complete, nil
}

// liveFacts re-reads ids and keeps those unchanged since they were planned: same seq and same tags. An
// edit or a retag bumps the seq, so a changed fact is treated as gone.
func liveFacts(
	ctx context.Context, tx store.Tx, b domain.BankRef, ids []domain.UnitID, expect map[domain.UnitID]domain.Unit,
) (map[domain.UnitID]domain.Unit, error) {
	facts, err := tx.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("re-read facts: %w", err)
	}
	live := map[domain.UnitID]domain.Unit{}
	for _, f := range facts {
		if p, ok := expect[f.ID]; ok && p.Seq == f.Seq && domain.SameTagSet(p.Tags, f.Tags) {
			live[f.ID] = f
		}
	}
	return live, nil
}

// stampable returns those of ids that are live and still cited in every scope of cited: the scopes the
// plan skipped because they already cited the unit, and the scopes this run wrote an observation citing it
// in. A concurrent clear, retag or invalidate may have deleted that observation since, resetting the unit
// without changing its seq (decision 1801). The caller holds tx.Seq, so no clear can commit in between.
func stampable(
	ctx context.Context, tx store.Tx, ids []domain.UnitID, live map[domain.UnitID]domain.Unit,
	cited map[domain.UnitID][]domain.ScopeKey,
) ([]domain.UnitID, error) {
	var out []domain.UnitID
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			continue
		}
		still := true
		for _, k := range cited[id] {
			ok, err := tx.Evidence().Cites(ctx, k, id)
			if err != nil {
				return nil, fmt.Errorf("cites %s in scope %q: %w", id, k.String(), err)
			}
			if !ok {
				still = false
				break
			}
		}
		if still {
			out = append(out, id)
		}
	}
	return out, nil
}

// target returns the observation an action writes: the existing one for an update (re-read in tx), a new
// one for a create. ok is false when the action must be skipped: a cited fact is not live, or the update's
// observation is gone or left the scope.
func (c *consolidator) target(
	ctx context.Context, tx store.Tx, s domain.ScopeKey, a ConsolidationAction, live map[domain.UnitID]domain.Unit,
) (domain.Unit, bool, error) {
	if len(a.Evidence) == 0 {
		return domain.Unit{}, false, nil
	}
	for _, e := range a.Evidence {
		if _, ok := live[e.Unit]; !ok {
			return domain.Unit{}, false, nil
		}
	}
	if a.Kind == ActionUpdate {
		cur, err := tx.GetUnits(ctx, s.BankRef, []domain.UnitID{a.ObservationID})
		if err != nil {
			return domain.Unit{}, false, fmt.Errorf("re-read observation %s: %w", a.ObservationID, err)
		}
		ok := len(cur) == 1 && cur[0].Type == domain.FactTypeObservation && domain.SameTagSet(cur[0].Tags, s.Tags)
		if !ok {
			return domain.Unit{}, false, nil
		}
		return cur[0], true, nil
	}
	obs := domain.Unit{ID: c.d.NewID(), Bank: s.BankRef, Type: domain.FactTypeObservation, Tags: s.Tags}
	for _, e := range a.Evidence {
		obs.OccurredStart = earliest(obs.OccurredStart, live[e.Unit].OccurredStart)
		obs.OccurredEnd = latest(obs.OccurredEnd, live[e.Unit].OccurredEnd)
		obs.MentionedAt = latest(obs.MentionedAt, live[e.Unit].MentionedAt)
	}
	return obs, true, nil
}

// writeObservation upserts obs with the action's text and embedding at the next version (1 for a new
// one), adds the evidence and records the version with every source the observation now cites.
func writeObservation(
	ctx context.Context, tx store.Tx, obs domain.Unit, a ConsolidationAction, seq int64, now time.Time,
) error {
	sources := slices.Clone(obs.SourceIDs)
	for _, e := range a.Evidence {
		if !slices.Contains(sources, e.Unit) {
			sources = append(sources, e.Unit)
		}
	}
	slices.Sort(sources)
	obs.Text, obs.Embedding, obs.Seq = a.Text, a.Embedding, seq
	obs.ProofCount, obs.Version = len(sources), obs.Version+1
	if err := tx.Units().UpsertObservation(ctx, obs); err != nil {
		return fmt.Errorf("upsert observation %s: %w", obs.ID, err)
	}
	if err := tx.Evidence().Add(ctx, obs.ID, a.Evidence); err != nil {
		return fmt.Errorf("evidence of %s: %w", obs.ID, err)
	}
	v := domain.ObservationVersion{Version: obs.Version, Text: obs.Text, SourceIDs: sources, At: now}
	if err := tx.Units().RecordObservationVersion(ctx, obs.Bank, obs.ID, v); err != nil {
		return fmt.Errorf("version of %s: %w", obs.ID, err)
	}
	return nil
}

// earliest returns the earlier of two optional times.
func earliest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.Before(*a)) {
		return b
	}
	return a
}

// latest returns the later of two optional times.
func latest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.After(*a)) {
		return b
	}
	return a
}
