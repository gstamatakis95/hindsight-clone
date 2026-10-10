package consolidate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

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
