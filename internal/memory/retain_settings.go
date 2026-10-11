package memory

import (
	"memd/internal/domain"
	"memd/internal/linking"
)

// Defaults for retain tuning knobs the spec leaves open (ServerDefaults leaves them unset).
// They follow Hindsight's defaults.
const (
	// defaultChunkMaxTokens is the chunk size (Hindsight: 3000 characters, about 750 tokens).
	defaultChunkMaxTokens = 750
	// defaultBatchChunks is how many chunks one retain transaction commits.
	defaultBatchChunks = 4
)

// tuning is RetainTuning with every default applied.
type tuning struct {
	chunkMax, chunkOverlap, structuredMax int
	matchThreshold                        float64
	causal                                bool
}

// tuningOf resolves cfg.Retain against the retain defaults.
func tuningOf(cfg domain.BankConfig) tuning {
	r := domain.RetainConfig{}
	if cfg.Retain != nil {
		r = *cfg.Retain
	}
	t := tuning{
		chunkMax:       positive(r.ChunkMaxTokens, defaultChunkMaxTokens),
		chunkOverlap:   nonNegative(r.ChunkOverlapTokens, 0),
		matchThreshold: floatOr(r.EntityMatchThreshold, linking.DefaultEntityMatchThreshold),
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
