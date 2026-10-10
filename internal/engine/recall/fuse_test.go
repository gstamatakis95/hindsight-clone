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
	got := NewRRF().Fuse(lists, 60, nil)
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
	// k <= 0 means 60; ties break by id.
	tie := NewRRF().Fuse(map[string][]Candidate{
		ArmSemantic: list(ArmSemantic, "y"), ArmKeyword: list(ArmKeyword, "x"),
	}, 0, nil)
	if want := []port.UnitID{"x", "y"}; !slices.Equal(fusedIDs(tie), want) || tie[0].RRF != 1.0/61 {
		t.Fatalf("tie = %v (%v)", fusedIDs(tie), tie[0].RRF)
	}
}

func TestFuseStrategyBoostRankSpace(t *testing.T) {
	lists := map[string][]Candidate{
		ArmSemantic: list(ArmSemantic, "a", "b"),
		ArmKeyword:  list(ArmKeyword, "c", "d"),
	}
	plain := NewRRF().Fuse(lists, 60, nil)
	if want := []port.UnitID{"a", "c", "b", "d"}; !slices.Equal(fusedIDs(plain), want) {
		t.Fatalf("plain = %v, want %v", fusedIDs(plain), want)
	}
	// keyword boost 4: c = 1/(60+1/4), d = 1/(60+2/4), both ahead of semantic's 1/61.
	boosted := NewRRF().Fuse(lists, 60, map[string]float64{ArmKeyword: 4, ArmGraph: -1})
	if want := []port.UnitID{"c", "d", "a", "b"}; !slices.Equal(fusedIDs(boosted), want) {
		t.Fatalf("boosted = %v, want %v", fusedIDs(boosted), want)
	}
	if want := 1 / (60 + 0.25); math.Abs(boosted[0].RRF-want) > 1e-12 {
		t.Fatalf("RRF(c) = %v, want %v", boosted[0].RRF, want)
	}
}
