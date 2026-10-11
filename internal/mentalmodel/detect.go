package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/recall"
	"memd/internal/store"
	"slices"
)

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
