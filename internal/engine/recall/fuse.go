package recall

import (
	"cmp"
	"maps"
	"slices"
)

// Fuser merges the arms' ranked lists into one.
type Fuser interface {
	// Fuse merges lists (keyed by arm name, each best first with Candidate.Arms[arm].Rank set) by
	// reciprocal-rank fusion with constant k (<= 0 = 60): RRF = sum over arms of 1 / (k + rank). The result
	// merges each unit's arm scores and is ordered by Candidate.RRF, best first; ties keep first-seen order
	// (arms in semantic, keyword, graph, temporal order, then rank), as Hindsight's stable sort does.
	Fuse(lists map[string][]Candidate, k int) []Candidate
}

// rrf is the reciprocal-rank-fusion Fuser.
type rrf struct{}

var _ Fuser = rrf{}

// newRRF returns the reciprocal-rank-fusion Fuser.
func newRRF() Fuser { return rrf{} }

// armOrder is the fixed order arms are summed in, so float sums and ties are deterministic.
var armOrder = []string{ArmSemantic, ArmKeyword, ArmGraph, ArmTemporal}

// Fuse implements Fuser.
func (rrf) Fuse(lists map[string][]Candidate, k int) []Candidate {
	if k <= 0 {
		k = defaultRRFK
	}
	byID := map[string]*Candidate{}
	var order []*Candidate
	for _, arm := range orderedArms(lists) {
		for i, c := range lists[arm] {
			s := armScore{Rank: i + 1}
			if a, ok := c.Arms[arm]; ok {
				s = a
			}
			m, ok := byID[string(c.ID)]
			if !ok {
				m = &Candidate{ID: c.ID, Unit: c.Unit, Arms: map[string]armScore{}}
				byID[string(c.ID)] = m
				order = append(order, m)
			}
			m.Arms[arm] = s
			m.RRF += 1 / (float64(k) + float64(s.Rank))
		}
	}
	out := make([]Candidate, len(order))
	for i, m := range order {
		out[i] = *m
	}
	slices.SortStableFunc(out, func(a, b Candidate) int { return cmp.Compare(b.RRF, a.RRF) })
	return out
}

// trimBoosted keeps at most limit (<= 0 = all) of the RRF-ordered fused candidates. Only when the pool
// exceeds the limit are candidates re-ordered by their boosted RRF score (Hindsight's stage-1 strategy
// boost): a boosted arm w contributes 1 / (k + rank / w) instead of 1 / (k + rank), so its rank r beats
// another arm's rank s exactly when r < w*s. Candidate.RRF itself stays unboosted.
func trimBoosted(cs []Candidate, limit, k int, boosts map[string]float64) []Candidate {
	if limit <= 0 || len(cs) <= limit {
		return cs
	}
	if k <= 0 {
		k = defaultRRFK
	}
	out := slices.Clone(cs)
	if len(boosts) > 0 {
		score := func(c Candidate) float64 { return boostedRRF(c, k, boosts) }
		slices.SortStableFunc(out, func(a, b Candidate) int { return cmp.Compare(score(b), score(a)) })
	}
	return out[:limit]
}

// boostedRRF is c's RRF score with every boosted arm's contribution recomputed in rank space.
func boostedRRF(c Candidate, k int, boosts map[string]float64) float64 {
	s := c.RRF
	for _, arm := range slices.Sorted(maps.Keys(boosts)) { // sorted: deterministic float sums
		w := boosts[arm]
		a, ok := c.Arms[arm]
		if !ok || w <= 0 || w == 1 {
			continue
		}
		r := float64(a.Rank)
		s += 1/(float64(k)+r/w) - 1/(float64(k)+r)
	}
	return s
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
