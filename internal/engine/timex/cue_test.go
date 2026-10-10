package timex

import "testing"

// TestHasTemporalCue covers rule-resolvable phrasing, vague phrasing and the lexical traps.
func TestHasTemporalCue(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"what did I do yesterday", true},
		{"last Tuesday", true},
		{"in March", true},
		{"March 5", true},
		{"5th of May", true},
		{"May 2024", true},
		{"between march and may", true},
		{"since 2021", true},
		{"the 90s", true},
		{"Q3", true},
		{"the past couple of weeks", true},
		{"a couple of weeks back", true},
		{"the day before my birthday", true},
		{"around the holidays", true},
		{"when I was in college", true},
		{"the other day", true},
		{"during the conference last fall", true},
		{"back in 2019", true},
		{"what happened after that", true},
		{"since then", true},
		{"may I ask", false},
		{"march on", false},
		{"a second opinion", false},
		{"spring water", false},
		{"fall down", false},
		{"Sunday roast", false},
		{"now that you mention it", false},
		{"I have 3 cats", false},
		{"the date field in the form", false},
		{"what is my name", false},
		{"since you asked", false},
		{"this may be wrong", false},
		{"is port 2019 open", false},
		{"I paid $2000", false},
		{"in her 90s", false},
		{"what were you up to", false},
		{"the last time I checked", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := hasTemporalCue(tt.query); got != tt.want {
			t.Errorf("hasTemporalCue(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// TestCueCoversRules checks the gate admits everything the rules resolve: a resolvable query the cue
// missed would silently lose its window.
func TestCueCoversRules(t *testing.T) {
	for _, row := range corpus() {
		ref := row.ref
		if ref.IsZero() {
			ref = testRef
		}
		if _, ok := parseRules(row.query, ref); ok && !hasTemporalCue(row.query) {
			t.Errorf("rules resolve %q but hasTemporalCue is false", row.query)
		}
	}
}

// TestVagueCuesNotResolvedByRules checks the vague cues reach the fallback rather than a rule.
func TestVagueCuesNotResolvedByRules(t *testing.T) {
	for _, q := range vagueCues {
		if !hasTemporalCue(q) {
			t.Errorf("hasTemporalCue(%q) = false", q)
		}
		if got, ok := parseRules(q, testRef); ok {
			t.Errorf("parseRules(%q) = %v, want no rule", q, got)
		}
	}
}
