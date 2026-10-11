package recall

import "memd/internal/domain"

// gate applies min_scores: arm floors go into retrieval, result floors after scoring.
type gate struct{}

// newScoreGate returns the gate.
func newScoreGate() gate { return gate{} }

// ArmFloor returns the retrieval floor of arm (semantic or keyword) and whether one is set.
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

// Keep reports whether c passes the result floors: reranker (only for a candidate the cross-encoder scored;
// Hindsight's rule for unscored candidates does not apply when rerank is off) and final.
func (gate) Keep(c candidate, m domain.MinScores) bool {
	// Compared as float32: the cross-encoder scores are float32, so a floor equal to a score keeps it.
	if m.Reranker != nil && c.Reranker != nil && float32(*c.Reranker) < float32(*m.Reranker) {
		return false
	}
	return m.Final == nil || c.Final >= *m.Final
}
