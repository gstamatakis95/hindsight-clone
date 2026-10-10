package recall

import (
	"math"
	"memd/internal/port"
	"slices"
	"testing"
)

// list builds an arm list of ids, ranked 1..n.
func list(name string, ids ...port.UnitID) []Candidate {
	return candidates(name, arm(name, ids...).hits, 0)
}

func fusedIDs(cs []Candidate) []port.UnitID {
	out := make([]port.UnitID, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestFuseRRFOrder(t *testing.T) {
	lists := map[string][]Candidate{
		ArmSemantic: list(ArmSemantic, "a", "b", "c"),
		ArmKeyword:  list(ArmKeyword, "b", "c", "d"),
		ArmGraph:    list(ArmGraph, "d"),
	}
	got := NewRRF().Fuse(lists, 60)
	// b = 1/61+1/62, d = 1/63+1/61, c = 1/63+1/62, a = 1/61.
	if want := []port.UnitID{"b", "d", "c", "a"}; !slices.Equal(fusedIDs(got), want) {
		t.Fatalf("order = %v, want %v", fusedIDs(got), want)
	}
	if want := 1.0/61 + 1.0/62; math.Abs(got[0].RRF-want) > 1e-12 {
		t.Fatalf("RRF(b) = %v, want %v", got[0].RRF, want)
	}
	if b := got[0].Arms; b[ArmSemantic].Rank != 2 || b[ArmKeyword].Rank != 1 || len(b) != 2 {
		t.Fatalf("arms of b = %+v", b)
	}
	// k <= 0 means 60; ties keep first-seen order (semantic before keyword), not id order.
	tie := NewRRF().Fuse(map[string][]Candidate{
		ArmSemantic: list(ArmSemantic, "y"), ArmKeyword: list(ArmKeyword, "x"),
	}, 0)
	if want := []port.UnitID{"y", "x"}; !slices.Equal(fusedIDs(tie), want) || tie[0].RRF != 1.0/61 {
		t.Fatalf("tie = %v (%v)", fusedIDs(tie), tie[0].RRF)
	}
}

func TestFuseStrategyBoostRankSpace(t *testing.T) {
	lists := map[string][]Candidate{
		ArmSemantic: list(ArmSemantic, "a", "b"),
		ArmKeyword:  list(ArmKeyword, "c", "d"),
	}
	fused := NewRRF().Fuse(lists, 60)
	if want := []port.UnitID{"a", "c", "b", "d"}; !slices.Equal(fusedIDs(fused), want) {
		t.Fatalf("fused = %v, want %v", fusedIDs(fused), want)
	}
	boosts := map[string]float64{ArmKeyword: 4, ArmGraph: -1}
	// A pool within the cap is not re-sorted: the boost only decides the cut.
	if got := trimBoosted(fused, 4, 60, boosts); !slices.Equal(fusedIDs(got), fusedIDs(fused)) {
		t.Fatalf("untrimmed = %v", fusedIDs(got))
	}
	// keyword boost 4 over the cap: c = 1/(60+1/4), d = 1/(60+2/4), both ahead of semantic's 1/61.
	got := trimBoosted(fused, 2, 60, boosts)
	if want := []port.UnitID{"c", "d"}; !slices.Equal(fusedIDs(got), want) {
		t.Fatalf("trimmed = %v, want %v", fusedIDs(got), want)
	}
	if got[0].RRF != 1.0/61 {
		t.Fatalf("RRF(c) = %v: the stored RRF must stay unboosted", got[0].RRF)
	}
	if want := 1 / (60 + 0.25); math.Abs(boostedRRF(got[0], 60, boosts)-want) > 1e-12 {
		t.Fatalf("boosted RRF(c) = %v, want %v", boostedRRF(got[0], 60, boosts), want)
	}
	if got := trimBoosted(fused, 2, 60, nil); !slices.Equal(fusedIDs(got), []port.UnitID{"a", "c"}) {
		t.Fatalf("no boosts = %v", fusedIDs(got))
	}
}
