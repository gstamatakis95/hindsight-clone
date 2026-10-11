package recall

import (
	"math"
	"testing"
)

func TestTrigramSimilarity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{name: "identical", a: "typescript", b: "typescript", want: 1},
		{name: "case and punctuation insensitive", a: "Type-Script", b: "type script", want: 1},
		{name: "disjoint", a: "abc", b: "xyz", want: 0},
		{name: "empty left", a: "", b: "abc", want: 0},
		{name: "empty both", a: "", b: "", want: 0},
		{name: "only separators", a: "--", b: "--", want: 0},
		// pg_trgm: SELECT similarity('word', 'two words') = 0.36363637.
		{name: "pg_trgm doc example", a: "word", b: "two words", want: 4.0 / 11.0},
		// 8 shared of 13 distinct trigrams.
		{name: "typo", a: "typscript", b: "typescript", want: 8.0 / 13.0},
		{name: "short words", a: "alise", b: "alice", want: 3.0 / 9.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := trigramSimilarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > 1e-6 {
				t.Fatalf("trigramSimilarity(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if rev := trigramSimilarity(tc.b, tc.a); rev != got {
				t.Fatalf("not symmetric: %v vs %v", got, rev)
			}
		})
	}
}
