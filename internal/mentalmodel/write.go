package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"time"
)

// Defaults used when the merged config leaves a mental-model knob unset (the server defaults set them).
const (
	defaultHistoryMaxEntries = 50
)

// write commits a successful run in one short transaction: last_refreshed_at always; the watermark and
// source_ids unless nothing was in scope (SPEC 2002: such a run only records that it ran); a new version,
// its history entry and a MENTAL_MODEL_REFRESHED event when the content changed. It returns the version
// written (0 = none). It takes tx.Seq first, so the bank row lock serializes it with every other
// read-modify-write of the model row. A model changed since the run read it (cleared, its query or scope
// edited, or another version written) is domain.ErrConflict and nothing is written; the job retries.
func (s *Service) write(
	ctx context.Context, p *refreshPlan, dr drafted, opts refreshOptions, trace domain.RefreshTrace,
) (int64, error) {
	now := s.d.Clock.Now()
	var version int64
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		version = 0
		seq, err := tx.Seq(ctx, p.m.Bank)
		if err != nil {
			return err
		}
		cur, err := tx.MentalModels().Get(ctx, p.m.Bank, p.m.Spec.ID)
		if err != nil {
			return err
		}
		if err := unchangedSince(p.m, cur); err != nil {
			return err
		}
		next := ranModel(cur, opts, trace, now)
		if dr.nothingInScope {
			return tx.MentalModels().Update(ctx, next)
		}
		next.LastSeqSeen, next.Stale = p.upTo, false
		if p.mode == domain.RefreshModeFull {
			if next.SourceIDs, err = prunedSources(ctx, tx, cur, dr); err != nil {
				return err
			}
		}
		if !dr.noNewFacts && dr.content != cur.ContentMD {
			version, err = writeVersion(ctx, tx, seq, cur, &next, dr, versionMeta{p.cfg, opts.OperationID, now})
			if err != nil {
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

// unchangedSince is domain.ErrConflict when cur moved since the run read read: another version, cleared
// content, a moved watermark, or an edited source query, read scope or response schema (the draft was
// made for the old ones).
func unchangedSince(read, cur domain.MentalModel) error {
	if cur.Version != read.Version || cur.ContentMD != read.ContentMD || cur.LastSeqSeen != read.LastSeqSeen ||
		scopeChanged(read.Spec, cur.Spec) ||
		cur.Spec.ResponseSchemaJSON != read.Spec.ResponseSchemaJSON {
		return fmt.Errorf("%w: mental model %s changed during the refresh", domain.ErrConflict, cur.Spec.ID)
	}
	return nil
}

// ranModel is cur after any successful run: last_refreshed_at set, the pause cleared by a manual run and
// the trace kept when keep_trace.
func ranModel(
	cur domain.MentalModel, opts refreshOptions, trace domain.RefreshTrace, now time.Time,
) domain.MentalModel {
	next := cur
	next.LastRefreshedAt = &now
	if opts.Manual {
		next.AutoRefreshPaused = false
	}
	if cur.Spec.KeepTrace {
		t := trace
		next.LastTrace = &t
	}
	return next
}

// prunedSources is a full refresh's source_ids: the draft's, or, when it drafted nothing (the content
// stays), the model's sources that are still live, so provenance of kept content survives and dead ids go.
func prunedSources(ctx context.Context, tx store.Tx, cur domain.MentalModel, dr drafted) ([]domain.UnitID, error) {
	if !dr.noNewFacts {
		return slices.Clone(dr.sourceIDs), nil
	}
	if len(cur.SourceIDs) == 0 {
		return nil, nil
	}
	live, err := tx.GetUnits(ctx, cur.Bank, cur.SourceIDs)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: read sources: %w", err)
	}
	return unitIDs(live), nil
}

// versionMeta is what a version write records besides the draft: the config (history cap), the operation
// and the time.
type versionMeta struct {
	cfg domain.BankConfig
	op  domain.OperationID
	now time.Time
}

// writeVersion moves cur's content to history (when there is any: the first build and a rebuild after a
// Clear, which recorded the content itself, add no entry) and puts the draft into next, stamping the event
// with the transaction's change seq.
func writeVersion(
	ctx context.Context, tx store.Tx, seq int64, cur domain.MentalModel, next *domain.MentalModel, dr drafted,
	meta versionMeta,
) (int64, error) {
	if cur.ContentMD != "" {
		e := domain.MentalModelHistoryEntry{
			Kind: domain.HistoryKindVersion, Version: cur.Version, PreviousContent: cur.ContentMD,
			ChangedAt: meta.now, Outcome: domain.RefreshOutcomeContentWritten,
		}
		if err := tx.MentalModels().AppendHistory(ctx, cur.Bank, cur.Spec.ID, e, historyKeep(meta.cfg)); err != nil {
			return 0, err
		}
	}
	next.ContentMD, next.Version = dr.content, cur.Version+1
	next.SourceIDs = slices.Clone(dr.sourceIDs)
	next.StructuredOutputJSON = dr.structured
	if dr.embedding != nil {
		next.Embedding = dr.embedding
	}
	err := tx.Events().Append(ctx, domain.LifecycleEvent{
		Seq: seq, Type: domain.EventTypeMentalModelRefreshed, Bank: cur.Bank, At: meta.now,
		OperationID: meta.op, SubjectID: string(cur.Spec.ID),
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

// configLevels are the override levels of a refresh above the bank: the model's stored overrides, then the
// call's overrides.
func configLevels(m domain.MentalModel, call *domain.BankConfig) []domain.BankConfig {
	var levels []domain.BankConfig
	if m.Spec.Overrides != nil {
		levels = append(levels, *m.Spec.Overrides)
	}
	if call != nil {
		levels = append(levels, *call)
	}
	return levels
}

// effectiveStrategy is the call's strategy, else the spec's, else the bank default, else agentic.
func effectiveStrategy(
	call domain.RefreshStrategy, m domain.MentalModel, cfg domain.BankConfig,
) domain.RefreshStrategy {
	switch {
	case call != domain.RefreshStrategyUnspecified:
		return call
	case m.Spec.Strategy != domain.RefreshStrategyUnspecified:
		return m.Spec.Strategy
	case cfg.MentalModels != nil && cfg.MentalModels.DefaultStrategy != domain.RefreshStrategyUnspecified:
		return cfg.MentalModels.DefaultStrategy
	}
	return domain.RefreshStrategyAgentic
}

// requestedMode is the spec's mode, else the bank default, else full.
func requestedMode(m domain.MentalModel, cfg domain.BankConfig) domain.RefreshMode {
	if m.Spec.Mode != domain.RefreshModeUnspecified {
		return m.Spec.Mode
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
