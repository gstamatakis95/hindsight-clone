package consolidate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// previewScopeLimit is how many existing scopes Preview examines; beyond it the preview is incomplete.
const previewScopeLimit = 1000

// StrategyMatcher picks the first strategy whose patterns match a scope.
type StrategyMatcher interface {
	// Match returns the first strategy with a pattern matching s, and whether there is one.
	Match(strategies []domain.ConsolidationStrategy, s port.ScopeKey) (*domain.ConsolidationStrategy, bool)
	// Preview evaluates draft strategies against existing scopes without saving.
	Preview(ctx context.Context, b port.BankRef, draft []domain.ConsolidationStrategy) (StrategyPreview, error)
}

// StrategyPreview mirrors memory.v1.PreviewConsolidationStrategiesResponse.
type StrategyPreview struct {
	// Matches has one entry per draft strategy, in order.
	Matches []StrategyMatch
	// Complete is false when the bank has more scopes than the preview examined.
	Complete bool
}

// StrategyMatch mirrors memory.v1.StrategyMatch: which existing scopes one strategy would govern.
type StrategyMatch struct {
	// Strategy is the strategy name.
	Strategy string
	// MatchedScopes are the scopes this strategy wins (first match).
	MatchedScopes []port.ScopeKey
	// ShadowedScopes are scopes its patterns match but an earlier strategy already wins.
	ShadowedScopes []port.ScopeKey
}

// strategyMatcher implements StrategyMatcher.
type strategyMatcher struct {
	scopes ScopeLister
}

var _ StrategyMatcher = (*strategyMatcher)(nil)

// NewStrategyMatcher returns the StrategyMatcher. scopes lists a bank's existing observation scopes for
// Preview (a read port.Reader lacks, CCR WP-10-1); nil makes Preview fail with FAILED_PRECONDITION, while
// Match works without it.
func NewStrategyMatcher(scopes ScopeLister) StrategyMatcher {
	return &strategyMatcher{scopes: scopes}
}

// Match returns the first strategy in list order with a pattern matching s; the winner applies whole
// (a later strategy never fills its gaps).
func (m *strategyMatcher) Match(
	strategies []domain.ConsolidationStrategy, s port.ScopeKey,
) (*domain.ConsolidationStrategy, bool) {
	for i := range strategies {
		if strategyClaims(strategies[i], s.Tags) {
			st := strategies[i]
			return &st, true
		}
	}
	return nil, false
}

// strategyClaims reports whether any pattern of st matches a scope with tags.
func strategyClaims(st domain.ConsolidationStrategy, tags []string) bool {
	for _, p := range st.Scopes {
		if patternMatches(p, tags) {
			return true
		}
	}
	return false
}

// patternMatches compares a scope's tags against one pattern with domain.MatchTags. Unspecified mode is
// all_strict: the scope holds every pattern tag (others allowed) and the shared scope never matches, as
// in Hindsight. A pattern with no tags matches only under exact (the shared scope); otherwise it is
// ignored, so an empty pattern never claims every scope. An explicit any or all pattern follows
// MatchTags, which admits untagged units, so it also matches the shared scope; use any_strict or
// all_strict (or leave the mode unset) to exclude it.
func patternMatches(p domain.ScopePattern, tags []string) bool {
	mode := p.TagsMatch
	if mode == domain.TagsMatchUnspecified {
		mode = domain.TagsMatchAllStrict
	}
	if len(domain.NormalizeTags(p.Tags)) == 0 && mode != domain.TagsMatchExact {
		return false
	}
	return domain.MatchTags(tags, p.Tags, mode)
}

// Preview lists up to previewScopeLimit existing scopes and reports, per draft strategy, the scopes it
// would win and those an earlier strategy shadows.
func (m *strategyMatcher) Preview(
	ctx context.Context, b port.BankRef, draft []domain.ConsolidationStrategy,
) (StrategyPreview, error) {
	if m.scopes == nil {
		return StrategyPreview{}, fmt.Errorf("%w: consolidate: no scope lister wired", domain.ErrFailedPrecondition)
	}
	scopes, complete, err := m.scopes.Scopes(ctx, b, previewScopeLimit)
	if err != nil {
		return StrategyPreview{}, fmt.Errorf("consolidate: list scopes: %w", err)
	}
	out := StrategyPreview{Matches: make([]StrategyMatch, len(draft)), Complete: complete}
	for i := range draft {
		out.Matches[i].Strategy = draft[i].Name
	}
	for _, s := range scopes {
		won := false
		for i := range draft {
			if !strategyClaims(draft[i], s.Tags) {
				continue
			}
			if won {
				out.Matches[i].ShadowedScopes = append(out.Matches[i].ShadowedScopes, s)
				continue
			}
			out.Matches[i].MatchedScopes = append(out.Matches[i].MatchedScopes, s)
			won = true
		}
	}
	return out, nil
}
