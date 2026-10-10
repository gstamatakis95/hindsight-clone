package recall

import "memd/internal/domain"

// Defaults recall owns: the spec states no server default for these knobs (decision 900), or they are not
// config knobs at all.
const (
	// DefaultMaxTokens is the packing budget when a request leaves max_tokens at 0.
	DefaultMaxTokens = 4096
	// DefaultChunksMaxTokens is the include.chunks budget when unset (Hindsight's 8192).
	DefaultChunksMaxTokens = 8192
	// DefaultSourceFactsMaxTokens is the include.source_facts budget when unset (Hindsight's 4096).
	DefaultSourceFactsMaxTokens = 4096
	// MaxQueryTokens is the query length limit; a longer query is cut to it.
	MaxQueryTokens = 500

	defaultRRFK                  = 60
	defaultRerankerMaxCandidates = 300
	defaultRecencyAlpha          = 0.2
	defaultTemporalAlpha         = 0.2
	defaultProofAlpha            = 0.1
	defaultRecencyHorizonDays    = 365
	defaultGraphMaxHops          = 1
	defaultTemporalBuckets       = 4
	defaultFuzzyTagMinSimilarity = 0.45
	defaultBudgetLow             = 100
	defaultBudgetMid             = 300
	defaultBudgetHigh            = 1000
	defaultBudgetMin             = 20
	defaultBudgetMax             = 2000

	// graphSeedLimit and graphSeedMinSimilarity pick the graph arm's entry points (Hindsight's
	// GRAPH_SEED_LIMIT and graph_seed_min_similarity).
	graphSeedLimit         = 20
	graphSeedMinSimilarity = 0.3
	// traceTopIDs is how many ids ArmTrace.TopIDs keeps.
	traceTopIDs = 5
)

// settings is the effective RecallConfig with every default applied.
type settings struct {
	rerank             bool
	rerankMax          int
	rrfK               int
	recencyAlpha       float64
	temporalAlpha      float64
	proofAlpha         float64
	recencyHorizonDays int
	graphMaxHops       int
	temporalBuckets    int
	fuzzyMinSimilarity float64
	boosts             map[string]float64
	enabled            map[string]bool
}

// settingsOf reads the recall settings from an effective config. An empty enabled_strategies list (nil, or
// an explicit clear) runs all four arms.
func settingsOf(cfg domain.BankConfig) settings {
	rc := domain.RecallConfig{}
	if cfg.Recall != nil {
		rc = *cfg.Recall
	}
	s := settings{
		rerank:             rc.Rerank == nil || *rc.Rerank,
		rerankMax:          intOr(rc.RerankerMaxCandidates, defaultRerankerMaxCandidates),
		rrfK:               intOr(rc.RRFK, defaultRRFK),
		recencyAlpha:       floatOr(rc.RecencyAlpha, defaultRecencyAlpha),
		temporalAlpha:      floatOr(rc.TemporalAlpha, defaultTemporalAlpha),
		proofAlpha:         floatOr(rc.ProofAlpha, defaultProofAlpha),
		recencyHorizonDays: intOr(rc.RecencyHorizonDays, defaultRecencyHorizonDays),
		graphMaxHops:       intOr(rc.GraphMaxHops, defaultGraphMaxHops),
		temporalBuckets:    intOr(rc.TemporalBuckets, defaultTemporalBuckets),
		fuzzyMinSimilarity: floatOr(rc.FuzzyTagMinSimilarity, defaultFuzzyTagMinSimilarity),
		boosts:             rc.StrategyBoosts,
		enabled:            map[string]bool{},
	}
	if s.rrfK <= 0 {
		s.rrfK = defaultRRFK
	}
	if s.recencyHorizonDays <= 0 {
		s.recencyHorizonDays = defaultRecencyHorizonDays
	}
	s.graphMaxHops = min(max(s.graphMaxHops, 1), 2)
	names := rc.EnabledStrategies
	if len(names) == 0 {
		names = []string{ArmSemantic, ArmKeyword, ArmGraph, ArmTemporal}
	}
	for _, n := range names {
		s.enabled[n] = true
	}
	return s
}

// intOr returns *p, or def when p is nil.
func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// floatOr returns *p, or def when p is nil.
func floatOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}
