package retain

import (
	"context"
	"memd/internal/domain"
	"time"
)

// DateNormalizer resolves a fact's relative time expression into absolute dates. It is declared here by its
// consumer; timex.TimeNormalizer satisfies it.
type DateNormalizer interface {
	// Normalize resolves f.TimeExpression against ref and fills f.OccurredStart and f.OccurredEnd in place. It
	// never fails a retain on dates; only the caller's cancellation is returned.
	Normalize(ctx context.Context, cfg domain.BankConfig, f *domain.ExtractedFact, ref time.Time) error
}

// ScopeExpander turns a unit's tags and scope mode into the scopes it consolidates under. It is declared
// here by its consumer; consolidate.ScopeExpander satisfies it.
type ScopeExpander interface {
	// Expand returns the scopes for tags under s; ALL_COMBINATIONS beyond max_combinations returns
	// domain.ErrTooManyScopes.
	Expand(b domain.BankRef, tags []string, s domain.ObservationScopes) ([]domain.ScopeKey, error)
}

// ConfigMerger merges config levels, later levels winning field by field; config.Merge satisfies it.
type ConfigMerger func(levels ...domain.BankConfig) domain.BankConfig

// Engine-owned defaults for retain tuning knobs the spec leaves open (ServerDefaults leaves them unset).
// They follow Hindsight's defaults (decision 803).
const (
	// DefaultChunkMaxTokens is the chunk size (Hindsight: 3000 characters, about 750 tokens).
	DefaultChunkMaxTokens = 750
	// DefaultEntityMatchThreshold is the entity merge threshold (Hindsight: 0.6, inclusive).
	DefaultEntityMatchThreshold = 0.6
	// DefaultTemporalLinkWindowHours bounds temporal links (Hindsight: 24 h).
	DefaultTemporalLinkWindowHours = 24
	// DefaultSemanticLinkK is the semantic neighbours per unit and the semantic link cap (Hindsight: top 50).
	DefaultSemanticLinkK = 50
	// DefaultSemanticLinkMinSimilarity is the cosine floor of a semantic link (Hindsight: 0.7).
	DefaultSemanticLinkMinSimilarity = 0.7
	// DefaultTemporalLinkCap caps temporal links per unit (spec: 20).
	DefaultTemporalLinkCap = 20
	// DefaultBatchChunks is how many chunks one retain transaction commits.
	DefaultBatchChunks = 4
)

// tuning is RetainTuning with every default applied.
type tuning struct {
	chunkMax, chunkOverlap, structuredMax int
	matchThreshold                        float64
	temporalWindow                        time.Duration
	temporalCap, semanticK                int
	semanticMinSim                        float64
	causal                                bool
}

// tuningOf resolves cfg.Retain against the engine defaults.
func tuningOf(cfg domain.BankConfig) tuning {
	r := domain.RetainTuning{}
	if cfg.Retain != nil {
		r = *cfg.Retain
	}
	t := tuning{
		chunkMax:       positive(r.ChunkMaxTokens, DefaultChunkMaxTokens),
		chunkOverlap:   nonNegative(r.ChunkOverlapTokens, 0),
		matchThreshold: floatOr(r.EntityMatchThreshold, DefaultEntityMatchThreshold),
		temporalWindow: time.Duration(positive(r.TemporalLinkWindowHours, DefaultTemporalLinkWindowHours)) * time.Hour,
		temporalCap:    nonNegative(r.TemporalLinkCap, DefaultTemporalLinkCap),
		semanticK:      nonNegative(r.SemanticLinkK, DefaultSemanticLinkK),
		semanticMinSim: floatOr(r.SemanticLinkMinSimilarity, DefaultSemanticLinkMinSimilarity),
		causal:         r.ExtractCausalLinks == nil || *r.ExtractCausalLinks,
	}
	t.structuredMax = positive(r.StructuredChunkMaxTokens, t.chunkMax)
	return t
}

// positive returns *p when set and above zero, else def.
func positive(p *int, def int) int {
	if p != nil && *p > 0 {
		return *p
	}
	return def
}

// nonNegative returns *p when set and not negative, else def.
func nonNegative(p *int, def int) int {
	if p != nil && *p >= 0 {
		return *p
	}
	return def
}

// floatOr returns *p when set, else def.
func floatOr(p *float64, def float64) float64 {
	if p != nil {
		return *p
	}
	return def
}

// boolOr returns *p when set, else def.
func boolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
