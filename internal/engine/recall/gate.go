package recall

import "memd/internal/domain"

// ScoreGate applies min_scores: arm floors go into retrieval, result floors after scoring.
type ScoreGate interface {
	// ArmFloor returns the retrieval floor of arm (semantic or keyword) and whether one is set.
	ArmFloor(arm string, m domain.MinScores) (float64, bool)
	// Keep reports whether c passes the result floors: reranker (only for a candidate the cross-encoder
	// scored; Hindsight's rule for unscored candidates does not apply when rerank is off) and final.
	Keep(c Candidate, m domain.MinScores) bool
}

// gate is the ScoreGate.
type gate struct{}

var _ ScoreGate = gate{}

// newScoreGate returns the ScoreGate.
func newScoreGate() ScoreGate { return gate{} }

// ArmFloor implements ScoreGate.
func (gate) ArmFloor(arm string, m domain.MinScores) (float64, bool) {
	var p *float64
	switch arm {
	case ArmSemantic:
		p = m.Semantic
	case ArmKeyword:
		p = m.Keyword
	}
	if p == nil {
		return 0, false
	}
	return *p, true
}

// Keep implements ScoreGate.
func (gate) Keep(c Candidate, m domain.MinScores) bool {
	// Compared as float32: the cross-encoder scores are float32, so a floor equal to a score keeps it.
	if m.Reranker != nil && c.Reranker != nil && float32(*c.Reranker) < float32(*m.Reranker) {
		return false
	}
	return m.Final == nil || c.Final >= *m.Final
}
