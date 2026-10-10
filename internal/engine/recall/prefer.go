package recall

import (
	"memd/internal/domain"
	"memd/internal/port"
)

// ObservationPreference drops raw facts covered by a returned observation and backfills.
type ObservationPreference interface {
	// Apply packs ranked into maxTokens (counted with count, same rules as Packer) without the raw facts that
	// a returned observation cites (Unit.SourceIDs), whatever their rank, and backfills the freed budget from
	// the rest of ranked. Returned observations are found by packing to a fixed point: dropping facts can
	// only free budget, so the set of dropped facts only grows and the loop ends.
	Apply(ranked []Candidate, maxTokens int, count TokenCounter) []Candidate
}

// preference is the ObservationPreference.
type preference struct{}

var _ ObservationPreference = preference{}

// NewObservationPreference returns the ObservationPreference.
func NewObservationPreference() ObservationPreference { return preference{} }

// Apply implements ObservationPreference.
func (preference) Apply(ranked []Candidate, maxTokens int, count TokenCounter) []Candidate {
	facts := map[port.UnitID]bool{}
	for _, c := range ranked {
		if c.Unit != nil && c.Unit.Type != domain.FactTypeObservation {
			facts[c.ID] = true
		}
	}
	dropped := map[port.UnitID]bool{}
	skip := func(c Candidate) bool { return dropped[c.ID] }
	for {
		packed, _ := pack(ranked, maxTokens, count, skip)
		grew := false
		for _, c := range packed {
			if c.Unit == nil || c.Unit.Type != domain.FactTypeObservation {
				continue
			}
			for _, src := range c.Unit.SourceIDs {
				if facts[src] && !dropped[src] {
					dropped[src] = true
					grew = true
				}
			}
		}
		if !grew {
			return packed
		}
	}
}
