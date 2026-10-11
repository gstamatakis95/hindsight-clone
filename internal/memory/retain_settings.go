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
		chunkMax:       domain.PositiveOr(r.ChunkMaxTokens, defaultChunkMaxTokens),
		chunkOverlap:   domain.NonNegativeOr(r.ChunkOverlapTokens, 0),
		matchThreshold: domain.ValueOr(r.EntityMatchThreshold, linking.DefaultEntityMatchThreshold),
		causal:         domain.ValueOr(r.ExtractCausalLinks, true),
	}
	t.structuredMax = domain.PositiveOr(r.StructuredChunkMaxTokens, t.chunkMax)
	return t
}
