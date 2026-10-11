package timex

import (
	"memd/internal/domain"
	"regexp"
	"strconv"
	"time"
)

// Deterministic rules. parseRules normalizes the text, then tries each rule in priority order and returns
// the window of the first rule that resolves; within a rule the leftmost match wins. Priority runs from
// "the day before yesterday", through ranges (between, from-to, since, until, before, after), absolute
// dates, year-qualified months, seasons and quarters, decades and relative expressions, down to bare
// years. All arithmetic uses ref in ref.Location(); weeks start Monday; windows are half-open.

// match is one regexp match: the normalized text and its submatch indexes.
type match struct {
	s   string
	loc []int
}

// g returns submatch i, or "" when it did not participate.
func (m match) g(i int) string {
	if 2*i+1 >= len(m.loc) || m.loc[2*i] < 0 {
		return ""
	}
	return m.s[m.loc[2*i]:m.loc[2*i+1]]
}

// n returns submatch i as an int (0 when it is not a number).
func (m match) n(i int) int {
	v, err := strconv.Atoi(m.g(i))
	if err != nil {
		return 0
	}
	return v
}

// next returns the byte after the whole match, or 0 at the end of the text.
func (m match) next() byte {
	if m.loc[1] < len(m.s) {
		return m.s[m.loc[1]]
	}
	return 0
}

// resolver turns a match into a window; ok is false when the match is not a valid expression.
type resolver func(m match, ref time.Time) (domain.TimeWindow, bool)

// rule pairs a pattern with its resolver.
type rule struct {
	re *regexp.Regexp
	fn resolver
}

// r compiles a rule.
func r(pattern string, fn resolver) rule {
	return rule{re: regexp.MustCompile(pattern), fn: fn}
}

// parseRules scans expr (a whole query or an extracted expression) for a recognized time expression and
// returns its window; ok is false when no rule resolves. A resolved window with a bound outside years
// 1-9999 ("999 decades ago") is consumed as no constraint: nil window with ok true, so no fallback call.
func parseRules(expr string, ref time.Time) (*domain.TimeWindow, bool) {
	s := normalize(expr)
	if s == "" {
		return nil, false
	}
	for _, set := range [][]rule{relDayRules, rangeRules, pointRules} {
		if w, ok := scan(set, s, ref, false); ok {
			if !inYearRange(w) {
				return nil, true
			}
			return &w, true
		}
	}
	return nil, false
}

// scan tries rules in order; anchored accepts only a match starting at offset 0.
func scan(rules []rule, s string, ref time.Time, anchored bool) (domain.TimeWindow, bool) {
	for _, ru := range rules {
		for _, loc := range ru.re.FindAllStringSubmatchIndex(s, -1) {
			if anchored && loc[0] != 0 {
				break
			}
			if w, ok := ru.fn(match{s: s, loc: loc}, ref); ok {
				return w, true
			}
		}
	}
	return domain.TimeWindow{}, false
}

// relDayRules run before ranges so "the day after tomorrow" is not read as "after tomorrow".
var relDayRules = []rule{
	r(`\b(?:the\s+)?day\s+before\s+yesterday\b`, fixedDay(-2)),
	r(`\b(?:the\s+)?day\s+after\s+tomorrow\b`, fixedDay(2)),
}

// pointRules resolve a single period; ranges reuse them for their endpoints.
var pointRules = concat(relDayRules, dateRules, qualifiedRules, relativeRules, namedRules, yearRules)

// dateRules are absolute calendar days.
var dateRules = []rule{
	// An ISO date may carry a time suffix ("2024-03-05t14:30:00z"); only the day is kept.
	r(`\b(\d{4})[-/](\d{1,2})[-/](\d{1,2})(?:\b|t\d)`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return dateWindow(m.n(1), m.n(2), m.n(3), ref)
	}),
	r(`\b(\d{1,2})/(\d{1,2})/(\d{4}|\d{2})\b`, resolveSlashDate),
	r(`\b(`+monthAny+`)\s+(\d{1,2})`+ordinal+`\s*-\s*(\d{1,2})`+ordinal+`\s+(\d{4})\b`, resolveDayRange),
	r(`\b(`+monthAny+`)\s+(\d{1,2})`+ordinal+`\s+(\d{4})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return dateWindow(m.n(3), int(monthNames[m.g(1)]), m.n(2), ref)
	}),
	r(`\b(\d{1,2})`+ordinal+`\s+(?:of\s+)?(`+monthAny+`)\s+(\d{4})\b`,
		func(m match, ref time.Time) (domain.TimeWindow, bool) {
			return dateWindow(m.n(3), int(monthNames[m.g(2)]), m.n(1), ref)
		}),
}

// qualifiedRules are months, quarters, seasons and decades pinned to a year.
var qualifiedRules = []rule{
	r(`\b(\d{4})-(\d{1,2})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		if b := m.next(); b == '-' || b == '/' || m.n(2) < 1 || m.n(2) > 12 || !explicitYear(m.n(1)) {
			return domain.TimeWindow{}, false
		}
		return monthWindow(m.n(1), time.Month(m.n(2)), ref.Location()), true
	}),
	r(`\bq([1-4])\s+(?:of\s+)?(\d{4})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return quarterYear(m.n(2), m.n(1), ref)
	}),
	r(`\b(\d{4})\s+q([1-4])\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return quarterYear(m.n(1), m.n(2), ref)
	}),
	r(`\b(first|second|third|fourth|1st|2nd|3rd|4th)\s+quarter\s+(?:of\s+)?(\d{4})\b`,
		func(m match, ref time.Time) (domain.TimeWindow, bool) {
			return quarterYear(m.n(2), ordinalWords[m.g(1)], ref)
		}),
	r(`\b(`+monthAny+`)\s+(?:of\s+)?(\d{4})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		if !explicitYear(m.n(2)) {
			return domain.TimeWindow{}, false
		}
		return monthWindow(m.n(2), monthNames[m.g(1)], ref.Location()), true
	}),
	r(`\b(`+seasons+`)\s+(?:of\s+)?(\d{4})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		if !explicitYear(m.n(2)) {
			return domain.TimeWindow{}, false
		}
		return seasonWindow(seasonStart[m.g(1)], m.n(2), ref.Location()), true
	}),
	r(`\b(`+seasons+`|`+monthAny+`)\s+(?:of\s+)?'(\d{2})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		y := twoDigitYear(m.n(2), ref)
		if first, ok := seasonStart[m.g(1)]; ok {
			return seasonWindow(first, y, ref.Location()), true
		}
		return monthWindow(y, monthNames[m.g(1)], ref.Location()), true
	}),
	r(`\b(\d{3}0)'?s\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		if !explicitYear(m.n(1)) {
			return domain.TimeWindow{}, false
		}
		return decadeWindow(m.n(1), ref.Location()), true
	}),
	r(`\bthe\s+'?(\d0)'?s\b`, resolveShortDecade),
}

// explicitYear bounds a year written next to a month, season, quarter or day.
func explicitYear(y int) bool {
	return y >= 1000 && y <= 9999
}

// plausibleYear bounds a bare year: within 120 years before and 20 after ref (Hindsight's range).
func plausibleYear(y int, ref time.Time) bool {
	return y >= ref.Year()-120 && y <= ref.Year()+20
}

// yearRules resolve a bare four-digit year ("in 2023", "2023") to the calendar year.
var yearRules = []rule{
	r(`\b(\d{4})\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		y := m.n(1)
		if !plausibleYear(y, ref) || !plausibleYearToken(m.s, m.loc[2], m.loc[3]) {
			return domain.TimeWindow{}, false
		}
		return yearWindow(y, ref.Location()), true
	}),
}

// fixedDay resolves to the day offset days from ref's day.
func fixedDay(offset int) resolver {
	return func(_ match, ref time.Time) (domain.TimeWindow, bool) {
		return dayWindow(addDays(startOfDay(ref), offset)), true
	}
}

// dateWindow is the day y-m-d when it is a real date.
func dateWindow(y, m, d int, ref time.Time) (domain.TimeWindow, bool) {
	if !explicitYear(y) || !validDate(y, m, d) {
		return domain.TimeWindow{}, false
	}
	return dayWindow(day(y, time.Month(m), d, ref.Location())), true
}

// resolveDayRange is "March 3-5 2024": [March 3, March 6).
func resolveDayRange(m match, ref time.Time) (domain.TimeWindow, bool) {
	mo := int(monthNames[m.g(1)])
	first, okFirst := dateWindow(m.n(4), mo, m.n(2), ref)
	last, okLast := dateWindow(m.n(4), mo, m.n(3), ref)
	if !okFirst || !okLast || last.Start.Before(first.Start) {
		return domain.TimeWindow{}, false
	}
	return domain.TimeWindow{Start: first.Start, End: last.End}, true
}

// twoDigitYear pivots yy around ref: the year in ref's century, or the century before when that would be
// more than 20 years after ref (ref 2026: 46 -> 2046, 47 -> 1947).
func twoDigitYear(yy int, ref time.Time) int {
	y := ref.Year() - ref.Year()%100 + yy
	if y > ref.Year()+20 {
		y -= 100
	}
	return y
}

// resolveSlashDate reads a/b/y as US month/day/year, or day/month/year when a > 12; a two-digit year
// pivots around ref (twoDigitYear).
func resolveSlashDate(m match, ref time.Time) (domain.TimeWindow, bool) {
	a, b, y := m.n(1), m.n(2), m.n(3)
	if len(m.g(3)) == 2 {
		y = twoDigitYear(y, ref)
	}
	if a > 12 {
		a, b = b, a
	}
	return dateWindow(y, a, b, ref)
}

// quarterYear is quarter q of year y.
func quarterYear(y, q int, ref time.Time) (domain.TimeWindow, bool) {
	if !explicitYear(y) || q < 1 || q > 4 {
		return domain.TimeWindow{}, false
	}
	return quarterWindow(y, q, ref.Location()), true
}

// resolveShortDecade reads "the 90s" as the most recent such decade starting no later than ref's year.
func resolveShortDecade(m match, ref time.Time) (domain.TimeWindow, bool) {
	start := 2000 + m.n(1)
	for start > ref.Year() {
		start -= 100
	}
	return decadeWindow(start, ref.Location()), true
}

// concat joins rule sets in order.
func concat(sets ...[]rule) []rule {
	var out []rule
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}
