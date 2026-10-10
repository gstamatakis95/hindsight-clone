package recall

import "memd/internal/domain"

// ScoreGate applies min_scores: arm floors go into retrieval, result floors after scoring.
type ScoreGate interface {
	// ArmFloor returns the retrieval floor of arm (semantic or keyword) and whether one is set.
	ArmFloor(arm string, m domain.MinScores) (float64, bool)
	// Keep reports whether c passes the result floors (reranker, final).
	Keep(c Candidate, m domain.MinScores) bool
}
