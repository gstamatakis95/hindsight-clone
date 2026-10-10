package consolidate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// applyInput is one batch's decision to write.
type applyInput struct {
	scope   port.ScopeKey
	actions []ConsolidationAction
	batch   []port.UnitID // the batch's facts
	stamp   []port.UnitID // the facts whose last scope this is: stamped in this transaction
}

// apply writes one batch's decision in one short transaction with no model call: tx.Seq, then each
// action (observation row stamped with the seq, evidence with exact quotes, an observation_versions row),
// then consolidated_at on the facts this scope finishes. Facts removed since decide (invalidated,
// replaced) are dropped from the evidence and the stamps. It reports whether an observation was written.
func (c *consolidator) apply(ctx context.Context, in applyInput) (bool, error) {
	var wrote bool
	err := c.d.Store.InTx(ctx, func(tx port.Tx) error {
		wrote = false
		now := c.d.Clock.Now()
		facts, err := tx.GetUnits(ctx, in.scope.BankRef, in.batch)
		if err != nil {
			return fmt.Errorf("re-read facts: %w", err)
		}
		live := map[port.UnitID]domain.Unit{}
		for _, f := range facts {
			live[f.ID] = f
		}
		var seq int64
		for _, a := range in.actions {
			ev := liveEvidence(a.Evidence, live)
			if len(ev) == 0 {
				continue
			}
			if seq == 0 {
				if seq, err = tx.Seq(ctx, in.scope.BankRef); err != nil {
					return fmt.Errorf("seq: %w", err)
				}
			}
			a.Evidence = ev
			if err := c.write(ctx, tx, in.scope, a, live, seq, now); err != nil {
				return err
			}
			wrote = true
		}
		var stamp []port.UnitID
		for _, id := range in.stamp {
			if _, ok := live[id]; ok {
				stamp = append(stamp, id)
			}
		}
		if len(stamp) == 0 {
			return nil
		}
		return tx.Units().MarkConsolidated(ctx, stamp, now)
	})
	if err != nil {
		return false, fmt.Errorf("consolidate: apply in scope %q: %w", in.scope.String(), err)
	}
	return wrote, nil
}

// liveEvidence keeps the evidence whose fact is still live.
func liveEvidence(ev []domain.Evidence, live map[port.UnitID]domain.Unit) []domain.Evidence {
	var out []domain.Evidence
	for _, e := range ev {
		if _, ok := live[e.Unit]; ok {
			out = append(out, e)
		}
	}
	return out
}

// write applies one action. An update whose observation is gone or left the scope is written as a new
// observation instead, so the facts' knowledge is not dropped.
func (c *consolidator) write(
	ctx context.Context, tx port.Tx, s port.ScopeKey, a ConsolidationAction,
	live map[port.UnitID]domain.Unit, seq int64, now time.Time,
) error {
	if a.Kind == ActionUpdate {
		cur, err := tx.GetUnits(ctx, s.BankRef, []port.UnitID{a.ObservationID})
		if err != nil {
			return fmt.Errorf("re-read observation %s: %w", a.ObservationID, err)
		}
		if len(cur) == 1 && cur[0].Type == domain.FactTypeObservation && domain.SameTagSet(cur[0].Tags, s.Tags) {
			return writeObservation(ctx, tx, cur[0], a, seq, now)
		}
	}
	obs := domain.Unit{
		ID: c.d.NewID(), Bank: s.BankRef, Type: domain.FactTypeObservation, Tags: s.Tags, Version: 0,
	}
	for _, e := range a.Evidence {
		obs.OccurredStart = earliest(obs.OccurredStart, live[e.Unit].OccurredStart)
		obs.OccurredEnd = latest(obs.OccurredEnd, live[e.Unit].OccurredEnd)
		obs.MentionedAt = latest(obs.MentionedAt, live[e.Unit].MentionedAt)
	}
	return writeObservation(ctx, tx, obs, a, seq, now)
}

// writeObservation upserts obs with the action's text and embedding at the next version (1 for a new
// one), adds the evidence and records the version with every source the observation now cites.
func writeObservation(
	ctx context.Context, tx port.Tx, obs domain.Unit, a ConsolidationAction, seq int64, now time.Time,
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
