package recall

import (
	"cmp"
	"maps"
	"slices"
)

// rrf merges the arms' ranked lists into one by reciprocal-rank fusion.
type rrf struct {
	// order is the arms' registration order: lists are summed in it, so float sums and ties are deterministic.
	order []string
}

// newRRF returns the fuser summing arms in order (the registration order).
func newRRF(order []string) rrf { return rrf{order: order} }

// Fuse merges lists (keyed by arm name, each best first with candidate.Arms[arm].Rank set) by reciprocal-rank
// fusion with constant k (<= 0 = 60): RRF = sum over arms of 1 / (k + rank). The result merges each unit's arm
// scores and is ordered by candidate.RRF, best first; ties keep first-seen order (arms in registration order, then
// rank), as Hindsight's stable sort does.
func (f rrf) Fuse(lists map[string][]candidate, k int) []candidate {
	if k <= 0 {
		k = defaultRRFK
	}
	byID := map[string]*candidate{}
	var order []*candidate
	for _, arm := range f.orderedArms(lists) {
		for i, c := range lists[arm] {
			s := armScore{Rank: i + 1}
			if a, ok := c.Arms[arm]; ok {
				s = a
			}
			m, ok := byID[string(c.ID)]
			if !ok {
				m = &candidate{ID: c.ID, Unit: c.Unit, Arms: map[string]armScore{}}
				byID[string(c.ID)] = m
				order = append(order, m)
			}
			m.Arms[arm] = s
			m.RRF += 1 / (float64(k) + float64(s.Rank))
		}
	}
	out := make([]candidate, len(order))
	for i, m := range order {
		out[i] = *m
	}
	slices.SortStableFunc(out, func(a, b candidate) int { return cmp.Compare(b.RRF, a.RRF) })
	return out
}

// trimBoosted keeps at most limit (<= 0 = all) of the RRF-ordered fused candidates. Only when the pool
// exceeds the limit are candidates re-ordered by their boosted RRF score (Hindsight's stage-1 strategy
// boost): a boosted arm w contributes 1 / (k + rank / w) instead of 1 / (k + rank), so its rank r beats
// another arm's rank s exactly when r < w*s. candidate.RRF itself stays unboosted.
func trimBoosted(cs []candidate, limit, k int, boosts map[string]float64) []candidate {
	if limit <= 0 || len(cs) <= limit {
		return cs
	}
	if k <= 0 {
		k = defaultRRFK
	}
	out := slices.Clone(cs)
	if len(boosts) > 0 {
		score := func(c candidate) float64 { return boostedRRF(c, k, boosts) }
		slices.SortStableFunc(out, func(a, b candidate) int { return cmp.Compare(score(b), score(a)) })
	}
	return out[:limit]
}

// boostedRRF is c's RRF score with every boosted arm's contribution recomputed in rank space.
func boostedRRF(c candidate, k int, boosts map[string]float64) float64 {
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

// orderedArms returns the arm names of lists: the registered arms in order, then any others sorted.
func (f rrf) orderedArms(lists map[string][]candidate) []string {
	var out, rest []string
	for _, a := range f.order {
		if _, ok := lists[a]; ok {
			out = append(out, a)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(lists)) {
		if !slices.Contains(f.order, a) {
			rest = append(rest, a)
		}
	}
	return append(out, rest...)
}
