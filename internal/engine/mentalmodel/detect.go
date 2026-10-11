package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/store"
	"slices"
)

// ChangeDetector decides whether a mental model is stale.
type ChangeDetector interface {
	// NeedsRefresh reports stale if an in-scope unit or observation has seq > m.LastSeqSeen, or a source
	// was retracted. upTo is the bank seq read first: the snapshot bound for the refresh.
	NeedsRefresh(ctx context.Context, m domain.MentalModel) (stale bool, upTo int64, err error)
}

// changeDetector is the ChangeDetector over the bank's change sequence.
type changeDetector struct {
	r     store.Reader
	scope ScopeReader
}

var _ ChangeDetector = (*changeDetector)(nil)

// NewChangeDetector returns the ChangeDetector. r reads BankSeq and the cited sources; s (store.Reader) finds the
// newest in-scope unit and resolves fuzzy scope tags.
func NewChangeDetector(r store.Reader, s ScopeReader) ChangeDetector {
	return &changeDetector{r: r, scope: s}
}

// NeedsRefresh reads BankSeq first, then looks for in-scope units in (LastSeqSeen, upTo] and for cited
// sources that are gone. Untagged units never stale a tagged model.
func (d *changeDetector) NeedsRefresh(ctx context.Context, m domain.MentalModel) (bool, int64, error) {
	upTo, err := d.r.BankSeq(ctx, m.Bank)
	if err != nil {
		return false, 0, fmt.Errorf("mentalmodel: bank seq: %w", err)
	}
	q, err := scopeQuery(ctx, d.scope, m, store.SeqUpTo(m.LastSeqSeen, upTo), true,
		recall.FuzzyTagMinSimilarity(domain.BankConfig{}))
	if err != nil {
		return false, 0, err
	}
	newest, err := d.scope.NewestSeqInScope(ctx, q)
	if err != nil {
		return false, 0, fmt.Errorf("mentalmodel: scope seq: %w", err)
	}
	if isStale(m, newest, false) {
		return true, upTo, nil
	}
	retracted, err := retractedSources(ctx, d.r, m)
	if err != nil {
		return false, 0, err
	}
	return isStale(m, newest, retracted), upTo, nil
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
