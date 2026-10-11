package recall

import "memd/internal/domain"

// gate applies the result floors of min_scores after scoring; each arm applies its own floor in retrieval
// (Query.ArmSettings).
type gate struct{}

// newScoreGate returns the gate.
func newScoreGate() gate { return gate{} }

// Keep reports whether c passes the result floors: reranker (only for a candidate the cross-encoder scored;
// Hindsight's rule for unscored candidates does not apply when rerank is off) and final.
func (gate) Keep(c candidate, m domain.MinScores) bool {
	// Compared as float32: the cross-encoder scores are float32, so a floor equal to a score keeps it.
	if m.Reranker != nil && c.Reranker != nil && float32(*c.Reranker) < float32(*m.Reranker) {
		return false
	}
	return m.Final == nil || c.Final >= *m.Final
}
