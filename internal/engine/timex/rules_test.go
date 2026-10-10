package timex

import (
	"testing"
	"time"
)

// TestParseRulesTrapSafe checks the rules alone (Normalize skips the cue gate) never fire on a none row.
func TestParseRulesTrapSafe(t *testing.T) {
	for _, row := range noneRows {
		if got, ok := parseRules(row.query, testRef); ok {
			t.Errorf("parseRules(%q) = %v, want no match", row.query, got)
		}
	}
}

// TestParseRulesExpressions covers extracted-expression shapes and edge cases beyond the query corpus.
func TestParseRulesExpressions(t *testing.T) {
	tests := []struct {
		expr string
		want [2]time.Time
	}{
		{"Feb 29", [2]time.Time{d(2024, 2, 29), d(2024, 3, 1)}},
		{"winter 2024", [2]time.Time{d(2024, 12, 1), d(2025, 3, 1)}},
		{"in the 2000s", [2]time.Time{d(2000, 1, 1), d(2010, 1, 1)}},
		{"three months ago", [2]time.Time{d(2026, 7, 1), d(2026, 8, 1)}},
		{"mid-March", [2]time.Time{d(2026, 3, 1), d(2026, 4, 1)}},
		{"2024/03/05", [2]time.Time{d(2024, 3, 5), d(2024, 3, 6)}},
		{"1/2/24", [2]time.Time{d(2024, 1, 2), d(2024, 1, 3)}},
		{"1/2/46", [2]time.Time{d(2046, 1, 2), d(2046, 1, 3)}},
		{"1/2/47", [2]time.Time{d(1947, 1, 2), d(1947, 1, 3)}},
		{"March 3–5, 2024", [2]time.Time{d(2024, 3, 3), d(2024, 3, 6)}},
		{"from March 5 to March 10, 2024", [2]time.Time{d(2024, 3, 5), d(2024, 3, 11)}},
		{"earlier today", [2]time.Time{d(2026, 10, 10), testRef}},
		{"the past month", [2]time.Time{d(2026, 9, 10), d(2026, 10, 11)}},
		{"next Saturday", [2]time.Time{d(2026, 10, 17), d(2026, 10, 18)}},
	}
	for _, tt := range tests {
		got, ok := parseRules(tt.expr, testRef)
		if !ok || !got.Start.Equal(tt.want[0]) || !got.End.Equal(tt.want[1]) {
			t.Errorf("parseRules(%q) = %v, %v; want %v", tt.expr, got, ok, tt.want)
		}
	}
}

// TestMonthEndClamp checks month and year shifts from a month's last day clamp instead of overflowing.
func TestMonthEndClamp(t *testing.T) {
	ref := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Time{
		"the past month":   d(2026, 2, 28),
		"last 3 months":    d(2025, 12, 31),
		"in the past year": d(2025, 3, 31),
	}
	for expr, start := range tests {
		got, ok := parseRules(expr, ref)
		if !ok || !got.Start.Equal(start) || !got.End.Equal(d(2026, 4, 1)) {
			t.Errorf("parseRules(%q) = %v, %v; want start %v", expr, got, ok, start)
		}
	}
	leap := time.Date(2024, 2, 29, 9, 0, 0, 0, time.UTC)
	if got, ok := parseRules("the past year", leap); !ok || !got.Start.Equal(d(2023, 2, 28)) {
		t.Errorf("past year from Feb 29 = %v, %v", got, ok)
	}
}

// TestParseRulesRejects checks invalid dates and implausible years do not resolve.
func TestParseRulesRejects(t *testing.T) {
	for _, expr := range []string{"2024-02-30", "13/13/2024", "in 3024", "February 30th", "from Bob to Alice"} {
		if got, ok := parseRules(expr, testRef); ok {
			t.Errorf("parseRules(%q) = %v, want no match", expr, got)
		}
	}
}

// TestNormalizeText checks punctuation folding.
func TestNormalizeText(t *testing.T) {
	tests := map[string]string{
		"March 5, 2024!":   "march 5 2024",
		"mid-March":        "mid march",
		"2020 - 2022":      "2020 - 2022",
		"the ’90s":         "the '90s",
		"  What's   up?  ": "what's up",
	}
	for in, want := range tests {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
