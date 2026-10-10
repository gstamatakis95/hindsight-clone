package recall

import (
	"cmp"
	"maps"
	"slices"
)

// Fuser merges the arms' ranked lists into one.
type Fuser interface {
	// Fuse merges lists (keyed by arm name, each best first with Candidate.Arms[arm].Rank set) by
	// reciprocal-rank fusion with constant k (<= 0 = 60): RRF = sum over arms of 1 / (k + rank / w), where w is
	// the arm's strategy boost (absent or <= 0 = 1). A boost divides the rank, as Hindsight does, so an arm
	// boosted by w places its rank r ahead of another arm's rank s exactly when r < w*s. The result merges each
	// unit's arm scores and is ordered by Candidate.RRF, best first (ties by id).
	Fuse(lists map[string][]Candidate, k int, boosts map[string]float64) []Candidate
}

// rrf is the reciprocal-rank-fusion Fuser.
type rrf struct{}

var _ Fuser = rrf{}

// NewRRF returns the reciprocal-rank-fusion Fuser.
func NewRRF() Fuser { return rrf{} }

// armOrder is the fixed order arms are summed in, so float sums are deterministic.
var armOrder = []string{ArmSemantic, ArmKeyword, ArmGraph, ArmTemporal}

// Fuse implements Fuser.
func (rrf) Fuse(lists map[string][]Candidate, k int, boosts map[string]float64) []Candidate {
	if k <= 0 {
		k = defaultRRFK
	}
	byID := map[string]*Candidate{}
	var order []*Candidate
	for _, arm := range orderedArms(lists) {
		w := boosts[arm]
		if w <= 0 {
			w = 1
		}
		for i, c := range lists[arm] {
			rank := i + 1
			s := ArmScore{Rank: rank}
			if a, ok := c.Arms[arm]; ok {
				s = a
				rank = a.Rank
			}
			m, ok := byID[string(c.ID)]
			if !ok {
				m = &Candidate{ID: c.ID, Unit: c.Unit, Arms: map[string]ArmScore{}}
				byID[string(c.ID)] = m
				order = append(order, m)
			}
			m.Arms[arm] = s
			m.RRF += 1 / (float64(k) + float64(rank)/w)
		}
	}
	out := make([]Candidate, len(order))
	for i, m := range order {
		out[i] = *m
	}
	slices.SortStableFunc(out, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.RRF, a.RRF), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// orderedArms returns the arm names of lists: the four known arms in armOrder, then any others sorted.
func orderedArms(lists map[string][]Candidate) []string {
	var out, rest []string
	for _, a := range armOrder {
		if _, ok := lists[a]; ok {
			out = append(out, a)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(lists)) {
		if !slices.Contains(armOrder, a) {
			rest = append(rest, a)
		}
	}
	return append(out, rest...)
}
