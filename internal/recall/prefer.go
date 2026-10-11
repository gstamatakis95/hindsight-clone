package recall

import (
	"memd/internal/domain"
)

// preference drops raw facts covered by an observation and backfills (prefer_observations).
type preference struct{}

// newObservationPreference returns the preference.
func newObservationPreference() preference { return preference{} }

// Apply follows Hindsight's prefer-observations dedup: every observation within the first window of ranked (window
// <= 0 = all of ranked; Recall passes 2 * the per-arm limit) supersedes the raw facts it cites (Unit.SourceIDs),
// which are dropped wherever they rank. The rest is packed into maxTokens (counted with count, same rules as
// packer.Pack), so the freed budget backfills from lower-ranked candidates. An observation below the window never
// drops a fact.
func (preference) Apply(ranked []candidate, window, maxTokens int, count tokenCounter) []candidate {
	head := ranked
	if window > 0 && len(head) > window {
		head = head[:window]
	}
	superseded := map[domain.UnitID]bool{}
	for _, c := range head {
		if c.Unit != nil && c.Unit.Type == domain.FactTypeObservation {
			for _, src := range c.Unit.SourceIDs {
				superseded[src] = true
			}
		}
	}
	skip := func(c candidate) bool {
		return c.Unit != nil && c.Unit.Type != domain.FactTypeObservation && superseded[c.ID]
	}
	out, _ := pack(ranked, maxTokens, count, skip)
	return out
}
