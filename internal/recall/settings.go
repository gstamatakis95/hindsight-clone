package recall

import "memd/internal/domain"

// Defaults recall owns: the server level sets no default for these knobs, or they are not
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
	defaultFuzzyTagMinSimilarity = 0.45
	defaultBudgetLow             = 100
	defaultBudgetMid             = 300
	defaultBudgetHigh            = 1000
	defaultBudgetMin             = 20
	defaultBudgetMax             = 2000

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
	fuzzyMinSimilarity float64
	boosts             map[string]float64
}

// FuzzyTagMinSimilarity is the trigram floor recall resolves fuzzy tag leaves with under cfg
// (recall.fuzzy_tag_min_similarity, default 0.45); the mental-model engine resolves model scopes with it.
func FuzzyTagMinSimilarity(cfg domain.BankConfig) float64 { return settingsOf(cfg).fuzzyMinSimilarity }

// settingsOf reads the engine's recall settings from an effective config. The arms read their own knobs
// (Query.ArmSettings).
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
		fuzzyMinSimilarity: floatOr(rc.FuzzyTagMinSimilarity, defaultFuzzyTagMinSimilarity),
		boosts:             rc.ArmBoosts,
	}
	if s.rrfK <= 0 {
		s.rrfK = defaultRRFK
	}
	if s.recencyHorizonDays <= 0 {
		s.recencyHorizonDays = defaultRecencyHorizonDays
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
