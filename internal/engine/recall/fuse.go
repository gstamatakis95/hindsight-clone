package recall

// Fuser merges the arms' ranked lists into one.
type Fuser interface {
	// Fuse merges lists (keyed by arm name) by reciprocal-rank fusion with constant k (default 60),
	// weighting arms by strategy_boosts; the result is ordered by Candidate.RRF, best first.
	Fuse(lists map[string][]Candidate, k int) []Candidate
}
