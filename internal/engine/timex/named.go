package timex

import (
	"memd/internal/domain"
	"time"
)

// Named periods without a year: weekdays, month days, seasons, months and quarters. A bare or
// preposition-led name ("in March", "in the fall", "Q3", "March 5", "on Tuesday") resolves to the most
// recent occurrence that starts no later than ref; "last" is the most recent one that has already ended
// (a weekday strictly before today, a month before the current one); "this" is the occurrence in the
// current week, year or season; "next" is the first one starting after ref (after today for weekdays).

// monthPreceders introduce a month name with no day or year; "this may" is excluded (modal verb).
const monthPreceders = `last|this|next|past|previous|coming|in|during|early|late|mid|throughout|of`

// seasonPreceders introduce a season name with no year.
const seasonPreceders = `last|this|next|past|previous|coming|in|during|over|through|throughout|early|late`

// namedRules resolve named periods; month days come first so "March 5" beats "in March".
var namedRules = []rule{
	r(`\b(`+monthAny+`)\s+(\d{1,2})`+ordinal+`\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return recentMonthDay(monthNames[m.g(1)], m.n(2), ref)
	}),
	r(`\b(?:the\s+)?(\d{1,2})`+ordinal+`\s+(?:of\s+)?(`+monthAny+`)\b`,
		func(m match, ref time.Time) (domain.TimeWindow, bool) {
			return recentMonthDay(monthNames[m.g(2)], m.n(1), ref)
		}),
	r(`\b(last|this|next|past|previous|coming|on)\s+(`+weekdays+`)s?\b`, resolveWeekday),
	r(`\b()(`+weekdays+`)\s+(?:morning|afternoon|evening|night)\b`, resolveWeekday),
	r(`\b(`+seasonPreceders+`)\s+(?:the\s+)?(`+seasons+`)\b`, resolveSeason),
	r(`\b(`+monthPreceders+`)\s+(?:the\s+month\s+of\s+)?(`+monthFull+`)\b`, resolveMonth),
	r(`\bq([1-4])\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return recentQuarter(m.n(1), ref), true
	}),
	r(`\b(first|second|third|fourth)\s+quarter\b`, func(m match, ref time.Time) (domain.TimeWindow, bool) {
		return recentQuarter(ordinalWords[m.g(1)], ref), true
	}),
}

// recentMonthDay is the most recent month-day not after ref's day (February 29 looks back up to 8 years).
func recentMonthDay(mo time.Month, d int, ref time.Time) (domain.TimeWindow, bool) {
	today := startOfDay(ref)
	for y := ref.Year(); y > ref.Year()-8; y-- {
		if t := day(y, mo, d, ref.Location()); validDate(y, int(mo), d) && !t.After(today) {
			return dayWindow(t), true
		}
	}
	return domain.TimeWindow{}, false
}

// resolveWeekday resolves a weekday name by its determiner (see the file comment); none or "on" means the
// most recent one not after today.
func resolveWeekday(m match, ref time.Time) (domain.TimeWindow, bool) {
	target := weekdayNames[m.g(2)]
	today := startOfDay(ref)
	back := (int(ref.Weekday()) - int(target) + 7) % 7
	switch m.g(1) {
	case "last", "past", "previous":
		if back == 0 {
			back = 7
		}
		return dayWindow(addDays(today, -back)), true
	case "next", "coming":
		ahead := (7 - back) % 7
		if ahead == 0 {
			ahead = 7
		}
		return dayWindow(addDays(today, ahead)), true
	case "this":
		return dayWindow(addDays(weekStart(ref), (int(target)+6)%7)), true
	default:
		return dayWindow(addDays(today, -back)), true
	}
}

// resolveSeason resolves a season name by its determiner; winter y spans December y to February y+1.
func resolveSeason(m match, ref time.Time) (domain.TimeWindow, bool) {
	first, loc := seasonStart[m.g(2)], ref.Location()
	occ := func(y int) domain.TimeWindow { return seasonWindow(first, y, loc) }
	y := ref.Year()
	switch m.g(1) {
	case "last", "past", "previous":
		for occ(y).End.After(ref) {
			y--
		}
	case "next", "coming":
		y--
		for !occ(y).Start.After(ref) {
			y++
		}
	case "this":
		if first == time.December && ref.Before(occ(y-1).End) {
			y--
		}
	default:
		for occ(y).Start.After(ref) {
			y--
		}
	}
	return occ(y), true
}

// resolveMonth resolves a month name by its determiner.
func resolveMonth(m match, ref time.Time) (domain.TimeWindow, bool) {
	mo := monthNames[m.g(2)]
	if mo == time.May && m.g(1) == "this" {
		return domain.TimeWindow{}, false
	}
	y := ref.Year()
	switch m.g(1) {
	case "last", "past", "previous":
		if mo >= ref.Month() {
			y--
		}
	case "next", "coming":
		if mo <= ref.Month() {
			y++
		}
	case "this":
	default:
		if mo > ref.Month() {
			y--
		}
	}
	return monthWindow(y, mo, ref.Location()), true
}

// recentQuarter is the most recent quarter q starting no later than ref.
func recentQuarter(q int, ref time.Time) domain.TimeWindow {
	y := ref.Year()
	if q > quarterOf(ref) {
		y--
	}
	return quarterWindow(y, q, ref.Location())
}
