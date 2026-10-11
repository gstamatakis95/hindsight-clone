package consolidate

import (
	"context"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
)

// previewScopeLimit is how many existing scopes Preview examines; beyond it the preview is incomplete.
const previewScopeLimit = 1000

// StrategyPreviewer shows which existing scopes draft consolidation strategies would govern (config.MatchStrategy
// picks the strategy of one scope).
type StrategyPreviewer interface {
	// Preview evaluates draft strategies against existing scopes without saving.
	Preview(ctx context.Context, b domain.BankRef, draft []domain.ConsolidationStrategy) (StrategyPreview, error)
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
	MatchedScopes []domain.ScopeKey
	// ShadowedScopes are scopes its patterns match but an earlier strategy already wins.
	ShadowedScopes []domain.ScopeKey
}

// strategyPreviewer implements StrategyPreviewer.
type strategyPreviewer struct {
	scopes ScopeLister
}

var _ StrategyPreviewer = (*strategyPreviewer)(nil)

// NewStrategyPreviewer returns the StrategyPreviewer. scopes (store.Reader) lists a bank's existing observation
// scopes; nil makes Preview fail with FAILED_PRECONDITION.
func NewStrategyPreviewer(scopes ScopeLister) StrategyPreviewer {
	return &strategyPreviewer{scopes: scopes}
}

// Preview lists up to previewScopeLimit existing scopes and reports, per draft strategy, the scopes it
// would win and those an earlier strategy shadows.
func (m *strategyPreviewer) Preview(
	ctx context.Context, b domain.BankRef, draft []domain.ConsolidationStrategy,
) (StrategyPreview, error) {
	if m.scopes == nil {
		return StrategyPreview{}, fmt.Errorf("%w: consolidate: no scope lister wired", domain.ErrFailedPrecondition)
	}
	infos, err := m.scopes.ListScopes(ctx, b, "", previewScopeLimit+1)
	if err != nil {
		return StrategyPreview{}, fmt.Errorf("consolidate: list scopes: %w", err)
	}
	complete := len(infos) <= previewScopeLimit
	infos = infos[:min(len(infos), previewScopeLimit)]
	scopes := make([]domain.ScopeKey, len(infos))
	for i, in := range infos {
		scopes[i] = domain.NewScopeKey(b, in.Scope.Tags)
	}
	out := StrategyPreview{Matches: make([]StrategyMatch, len(draft)), Complete: complete}
	for i := range draft {
		out.Matches[i].Strategy = draft[i].Name
	}
	for _, s := range scopes {
		won := false
		for i := range draft {
			if _, ok := config.MatchStrategy(draft[i:i+1], s); !ok {
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
