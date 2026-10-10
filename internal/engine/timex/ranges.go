package timex

import (
	"memd/internal/domain"
	"regexp"
	"time"
)

// Ranges. Each endpoint must be a point expression starting right after the keyword ("since you asked"
// is not a range). Endpoints may omit what the other side gives: "between March and May 2024" borrows
// 2024, and a bare month, season or weekday ("from March to May", "until Friday") reads as "in March" /
// "on Friday". Windows: between/from/since X to Y is [X.Start, Y.End); since X is [X.Start, ref); until X
// is [unbounded, X.End); before X is [unbounded, X.Start); after X is [X.End, unbounded). An endpoint "now"
// or "present" is the instant ref (so "from 2020 to now" ends at ref); "today" is the whole day.

// rangeRules run before point rules.
var rangeRules = []rule{
	r(`\bbetween\s+(.+?)\s+and\s+(.+)$`, resolveSpan),
	r(`\bfrom\s+(.+?)\s+(?:to|until|till|through|thru)\s+(.+)$`, resolveSpan),
	r(`\b(\d{4})\s*-\s*(\d{4})\b`, resolveYearSpan),
	r(`\bsince\s+(.+?)\s+(?:to|until|till|through|thru)\s+(.+)$`, resolveSpan),
	r(`\bsince\s+(.+)$`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		x, ok := endpoint(m.g(1), "", ref)
		if !ok || x.Start.IsZero() || !x.Start.Before(ref) {
			return domain.TimeWindow{}, false
		}
		return domain.TimeWindow{Start: x.Start, End: ref}, true
	}),
	r(`\b(?:until|till)\s+(.+)$`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		x, ok := endpoint(m.g(1), "", ref)
		return domain.TimeWindow{End: x.End}, ok && !x.End.IsZero()
	}),
	r(`\b(?:before|prior\s+to)\s+(.+)$`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		x, ok := endpoint(m.g(1), "", ref)
		return domain.TimeWindow{End: x.Start}, ok && !x.Start.IsZero()
	}),
	r(`\bafter\s+(.+)$`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		x, ok := endpoint(m.g(1), "", ref)
		return domain.TimeWindow{Start: x.End}, ok && !x.End.IsZero()
	}),
}

// yearToken finds a year an endpoint can borrow.
var yearToken = regexp.MustCompile(`\b\d{4}\b`)

// presentToken is an endpoint meaning the reference instant ("to now", "and present"); "today" is the
// whole day instead, through a point rule.
var presentToken = regexp.MustCompile(`^(?:right\s+now|now|the\s+present|present)\b`)

// resolveSpan is [X.Start, Y.End) for "between X and Y" and "from X to Y".
func resolveSpan(m match, ref time.Time) (domain.TimeWindow, bool) {
	y, ok := endpoint(m.g(2), "", ref)
	if !ok {
		return domain.TimeWindow{}, false
	}
	x, ok := endpoint(m.g(1), yearToken.FindString(m.g(2)), ref)
	if !ok || x.Start.IsZero() || y.End.IsZero() || !x.Start.Before(y.End) {
		return domain.TimeWindow{}, false
	}
	return domain.TimeWindow{Start: x.Start, End: y.End}, true
}

// resolveYearSpan is "2020-2022": [2020-01-01, 2023-01-01).
func resolveYearSpan(m match, ref time.Time) (domain.TimeWindow, bool) {
	a, b := m.n(1), m.n(2)
	if !plausibleYear(a, ref) || !plausibleYear(b, ref) || a >= b {
		return domain.TimeWindow{}, false
	}
	return domain.TimeWindow{Start: yearWindow(a, ref.Location()).Start, End: yearWindow(b, ref.Location()).End}, true
}

// endpoint resolves one side of a range: now or present as the instant ref, else text plus the borrowed
// year when text has none, else a point expression at the start of text, else text read as "in ..." or
// "on ...".
func endpoint(text, year string, ref time.Time) (domain.TimeWindow, bool) {
	if presentToken.MatchString(text) {
		return domain.TimeWindow{Start: ref, End: ref}, true
	}
	var cands []string
	if year != "" && yearToken.FindString(text) == "" {
		cands = append(cands, text+" "+year)
	}
	cands = append(cands, text, "in "+text, "on "+text)
	for _, c := range cands {
		if w, ok := scan(pointRules, c, ref, true); ok {
			return w, true
		}
	}
	return domain.TimeWindow{}, false
}
