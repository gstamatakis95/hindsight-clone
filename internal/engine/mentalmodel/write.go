package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// Defaults used when the merged config leaves a mental-model knob unset (the server defaults set them).
const (
	defaultHistoryMaxEntries = 50
	defaultDebounceSeconds   = 30
)

// write commits a successful run in one short transaction: the watermark and last_refreshed_at always; a
// new version, its history entry and a MENTAL_MODEL_REFRESHED event when the content changed. It returns
// the version written (0 = none). A model changed since the run read it (cleared, its query or scope
// edited, or another version written) is domain.ErrConflict and nothing is written; the job retries.
func (r *runner) write(
	ctx context.Context, p *refreshPlan, dr drafted, opts RefreshOptions, trace domain.RefreshTrace,
) (int64, error) {
	now := r.d.Clock.Now()
	var version int64
	err := r.d.Store.InTx(ctx, func(tx port.Tx) error {
		version = 0
		cur, err := tx.MentalModels().Get(ctx, p.m.Bank, p.m.Spec.ID)
		if err != nil {
			return err
		}
		if cur.Version != p.m.Version || cur.ContentMD != p.m.ContentMD || cur.LastSeqSeen != p.m.LastSeqSeen {
			return fmt.Errorf("%w: mental model %s changed during the refresh", domain.ErrConflict, cur.Spec.ID)
		}
		next := cur
		next.LastRefreshedAt, next.LastSeqSeen, next.IsStale = &now, p.upTo, false
		if opts.Manual {
			next.AutoRefreshPaused = false
		}
		if cur.Spec.Trigger.KeepTrace {
			t := trace
			next.LastTrace = &t
		}
		if !dr.noNewFacts && p.mode == domain.RefreshModeFull {
			next.SourceIDs = slices.Clone(dr.sourceIDs)
		}
		if !dr.noNewFacts && dr.content != cur.ContentMD {
			if version, err = writeVersion(ctx, tx, cur, &next, dr, p.cfg, opts.OperationID, now); err != nil {
				return err
			}
		}
		return tx.MentalModels().Update(ctx, next)
	})
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: write %s: %w", p.m.Spec.ID, err)
	}
	return version, nil
}

// writeVersion moves cur's content to history and puts the draft into next, stamping the event with the
// transaction's change seq.
func writeVersion(
	ctx context.Context, tx port.Tx, cur domain.MentalModel, next *domain.MentalModel, dr drafted,
	cfg domain.BankConfig, op port.OperationID, now time.Time,
) (int64, error) {
	seq, err := tx.Seq(ctx, cur.Bank)
	if err != nil {
		return 0, err
	}
	if cur.Version > 0 || cur.ContentMD != "" {
		e := domain.MentalModelHistoryEntry{
			Kind: domain.HistoryKindVersion, Version: cur.Version, PreviousContent: cur.ContentMD,
			ChangedAt: now, Outcome: domain.RefreshOutcomeContentWritten,
		}
		if err := tx.MentalModels().AppendHistory(ctx, cur.Bank, cur.Spec.ID, e, historyKeep(cfg)); err != nil {
			return 0, err
		}
	}
	next.ContentMD, next.Version = dr.content, cur.Version+1
	next.SourceIDs = slices.Clone(dr.sourceIDs)
	next.StructuredOutputJSON = dr.structured
	if dr.embedding != nil {
		next.Embedding = dr.embedding
	}
	err = tx.Events().Append(ctx, domain.LifecycleEvent{
		Seq: seq, Type: domain.EventTypeMentalModelRefreshed, Bank: cur.Bank, At: now,
		OperationID: op, SubjectID: string(cur.Spec.ID),
	})
	if err != nil {
		return 0, err
	}
	return next.Version, nil
}

// historyKeep is history_max_entries (per kind).
func historyKeep(cfg domain.BankConfig) int {
	if cfg.MentalModels != nil && cfg.MentalModels.HistoryMaxEntries != nil {
		return *cfg.MentalModels.HistoryMaxEntries
	}
	return defaultHistoryMaxEntries
}

// configLevels are the override levels of a refresh above the bank: the model's stored overrides, the
// trigger's knobs, then the call's overrides.
func configLevels(m domain.MentalModel, call *domain.BankConfig) []domain.BankConfig {
	var levels []domain.BankConfig
	if m.Spec.Overrides != nil {
		levels = append(levels, *m.Spec.Overrides)
	}
	if t := triggerLevel(m.Spec.Trigger); t != nil {
		levels = append(levels, *t)
	}
	if call != nil {
		levels = append(levels, *call)
	}
	return levels
}

// triggerLevel maps the trigger's reflect knobs onto a config level; nil when it sets none.
func triggerLevel(t domain.RefreshTrigger) *domain.BankConfig {
	rc := domain.ReflectConfig{
		RecallMaxTokens:                   t.RecallMaxTokens,
		SearchObservationsMaxTokens:       t.SearchObservationsMaxTokens,
		SearchObservationsIncludeEntities: t.SearchObservationsIncludeEntities,
	}
	if rc.RecallMaxTokens == nil && rc.SearchObservationsMaxTokens == nil && rc.SearchObservationsIncludeEntities == nil {
		return nil
	}
	return &domain.BankConfig{Reflect: &rc}
}

// effectiveStrategy is the call's strategy, else the trigger's, else the bank default, else agentic.
func effectiveStrategy(
	call domain.RefreshStrategy, m domain.MentalModel, cfg domain.BankConfig,
) domain.RefreshStrategy {
	switch {
	case call != domain.RefreshStrategyUnspecified:
		return call
	case m.Spec.Trigger.Strategy != domain.RefreshStrategyUnspecified:
		return m.Spec.Trigger.Strategy
	case cfg.MentalModels != nil && cfg.MentalModels.DefaultStrategy != domain.RefreshStrategyUnspecified:
		return cfg.MentalModels.DefaultStrategy
	}
	return domain.RefreshStrategyAgentic
}

// requestedMode is the trigger's mode, else the bank default, else full.
func requestedMode(m domain.MentalModel, cfg domain.BankConfig) domain.RefreshMode {
	if m.Spec.Trigger.Mode != domain.RefreshModeUnspecified {
		return m.Spec.Trigger.Mode
	}
	if cfg.MentalModels != nil && cfg.MentalModels.DefaultMode != domain.RefreshModeUnspecified {
		return cfg.MentalModels.DefaultMode
	}
	return domain.RefreshModeFull
}

// maxDeltaRatio is max_delta_ratio; 0 = no limit.
func maxDeltaRatio(cfg domain.BankConfig) float64 {
	if cfg.MentalModels != nil && cfg.MentalModels.MaxDeltaRatio != nil {
		return *cfg.MentalModels.MaxDeltaRatio
	}
	return 0
}
