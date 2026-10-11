package recall

import (
	"memd/internal/domain"
	"memd/internal/port"
)

// ObservationPreference drops raw facts covered by an observation and backfills.
type ObservationPreference interface {
	// Apply follows Hindsight's prefer-observations dedup: every observation within the first window of ranked
	// (window <= 0 = all of ranked; Recall passes 2 * the per-arm limit) supersedes the raw facts it cites
	// (Unit.SourceIDs), which are dropped wherever they rank. The rest is packed into maxTokens (counted with
	// count, same rules as Packer), so the freed budget backfills from lower-ranked candidates. An observation
	// below the window never drops a fact.
	Apply(ranked []Candidate, window, maxTokens int, count TokenCounter) []Candidate
}

// preference is the ObservationPreference.
type preference struct{}

var _ ObservationPreference = preference{}

// newObservationPreference returns the ObservationPreference.
func newObservationPreference() ObservationPreference { return preference{} }

// Apply implements ObservationPreference.
func (preference) Apply(ranked []Candidate, window, maxTokens int, count TokenCounter) []Candidate {
	head := ranked
	if window > 0 && len(head) > window {
		head = head[:window]
	}
	superseded := map[port.UnitID]bool{}
	for _, c := range head {
		if c.Unit != nil && c.Unit.Type == domain.FactTypeObservation {
			for _, src := range c.Unit.SourceIDs {
				superseded[src] = true
			}
		}
	}
	skip := func(c Candidate) bool {
		return c.Unit != nil && c.Unit.Type != domain.FactTypeObservation && superseded[c.ID]
	}
	out, _ := pack(ranked, maxTokens, count, skip)
	return out
}
