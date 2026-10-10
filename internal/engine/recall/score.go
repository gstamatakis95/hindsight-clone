package recall

import "context"

// Scorer applies the cross-encoder, then recency, temporal and proof boosts.
type Scorer interface {
	// Score reranks the top reranker_max_candidates of cs (RERANK slot), applies the boosts, sets
	// Candidate.Final and returns cs ordered by it, best first.
	Score(ctx context.Context, q *ParsedQuery, cs []Candidate) ([]Candidate, error)
}

// Boosts are the boosts the Scorer added to a candidate's base score.
type Boosts struct {
	// Recency favours recent units (recency_alpha over recency_horizon_days).
	Recency float64
	// Temporal favours units close to the query's time window (temporal_alpha).
	Temporal float64
	// Proof favours observations with many supporting facts (proof_alpha).
	Proof float64
}
