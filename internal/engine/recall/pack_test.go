package recall

import (
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
)

// words returns a candidate whose unit text has n words.
func words(id string, n int, typ domain.FactType, sources ...port.UnitID) Candidate {
	u := domain.Unit{ID: port.UnitID(id), Type: typ, Text: strings.TrimSpace(strings.Repeat("w ", n)), SourceIDs: sources}
	return Candidate{ID: u.ID, Unit: &u}
}

func candIDs(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c.ID)
	}
	return out
}

func TestPackSkipsOversized(t *testing.T) {
	cs := []Candidate{
		words("c1", 3, domain.FactTypeWorld), words("c2", 10, domain.FactTypeWorld), words("c3", 2, domain.FactTypeWorld),
		words("c4", 2, domain.FactTypeWorld),
	}
	got := NewPacker(wordCounter{}).Pack(cs, 6)
	if want := []string{"c1", "c3"}; !slices.Equal(candIDs(got), want) {
		t.Fatalf("packed = %v, want %v", candIDs(got), want)
	}
	if got[0].Tokens != 3 || got[1].Tokens != 2 {
		t.Fatalf("tokens = %d, %d", got[0].Tokens, got[1].Tokens)
	}
	// An exact fit is kept.
	if got := NewPacker(wordCounter{}).Pack(cs[:1], 3); len(got) != 1 {
		t.Fatalf("exact fit dropped: %v", candIDs(got))
	}
}

func TestPackFloorWhenNothingFits(t *testing.T) {
	cs := []Candidate{words("big1", 10, domain.FactTypeWorld), words("big2", 12, domain.FactTypeWorld)}
	got := NewPacker(wordCounter{}).Pack(cs, 5)
	if len(got) != 1 || got[0].ID != "big1" || got[0].Tokens != 10 {
		t.Fatalf("floor = %v (%d tokens), want big1 whole", candIDs(got), got[0].Tokens)
	}
	if got := NewPacker(wordCounter{}).Pack(nil, 5); len(got) != 0 {
		t.Fatalf("empty input packed %v", candIDs(got))
	}
	// max_tokens <= 0 = the default budget.
	if got := NewPacker(wordCounter{}).Pack(cs, 0); len(got) != 2 {
		t.Fatalf("default budget packed %v", candIDs(got))
	}
}

func TestPreferObservations(t *testing.T) {
	ranked := []Candidate{
		words("f1", 2, domain.FactTypeWorld),
		words("o1", 2, domain.FactTypeObservation, "f1", "f2"),
		words("f2", 2, domain.FactTypeExperience),
		words("f3", 2, domain.FactTypeWorld),
		words("f4", 2, domain.FactTypeWorld),
		words("o2", 2, domain.FactTypeObservation, "f4"),
	}
	if got := NewPacker(wordCounter{}).Pack(ranked, 6); !slices.Equal(candIDs(got), []string{"f1", "o1", "f2"}) {
		t.Fatalf("plain pack = %v", candIDs(got))
	}
	// Window 2 covers o1 only: f1 and f2 go, o2 (below the window) doesn't drop f4.
	got := NewObservationPreference().Apply(ranked, 2, 6, wordCounter{})
	if want := []string{"o1", "f3", "f4"}; !slices.Equal(candIDs(got), want) {
		t.Fatalf("prefer window 2 = %v, want %v", candIDs(got), want)
	}
	// The whole list as window: o2 supersedes f4 although o2 itself doesn't make the budget-ordered cut first.
	got = NewObservationPreference().Apply(ranked, 0, 6, wordCounter{})
	if want := []string{"o1", "f3", "o2"}; !slices.Equal(candIDs(got), want) {
		t.Fatalf("prefer whole window = %v, want %v", candIDs(got), want)
	}
}

func TestTruncateTokens(t *testing.T) {
	c := wordCounter{}
	tests := []struct {
		text string
		n    int
		want string
	}{
		{"one two three", 5, "one two three"},
		{"one two three", 2, "one two"},
		{"one  two\tthree four", 3, "one  two\tthree"},
		{"one two", 0, ""},
	}
	for _, tt := range tests {
		if got := truncateTokens(tt.text, tt.n, c); got != tt.want {
			t.Errorf("truncateTokens(%q, %d) = %q, want %q", tt.text, tt.n, got, tt.want)
		}
	}
	// A single word over budget is cut mid-word with a char counter.
	if got := truncateTokens("abcdef", 3, charCounter{}); got != "abc" {
		t.Errorf("mid-word cut = %q", got)
	}
}

// charCounter counts runes.
type charCounter struct{}

func (charCounter) Count(s string) int { return len([]rune(s)) }
