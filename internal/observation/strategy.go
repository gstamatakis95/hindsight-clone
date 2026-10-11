package observation

import (
	"context"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
)

// previewScopeLimit is how many existing scopes the strategy preview examines; beyond it the preview is
// incomplete.
const previewScopeLimit = 1000

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

// PreviewConsolidationStrategies evaluates draft strategies against the bank's existing observation scopes
// without saving: it lists up to previewScopeLimit scopes and reports, per draft strategy, the scopes it would
// win (config.MatchStrategy picks the first match) and those an earlier strategy shadows.
func (s *Service) PreviewConsolidationStrategies(
	ctx context.Context, b domain.BankRef, draft []domain.ConsolidationStrategy,
) (StrategyPreview, error) {
	infos, err := s.d.DB.Read().ListScopes(ctx, b, "", previewScopeLimit+1)
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
	for _, sc := range scopes {
		won := false
		for i := range draft {
			if _, ok := config.MatchStrategy(draft[i:i+1], sc); !ok {
				continue
			}
			if won {
				out.Matches[i].ShadowedScopes = append(out.Matches[i].ShadowedScopes, sc)
				continue
			}
			out.Matches[i].MatchedScopes = append(out.Matches[i].MatchedScopes, sc)
			won = true
		}
	}
	return out, nil
}
