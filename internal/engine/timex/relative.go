package timex

import (
	"memd/internal/domain"
	"time"
)

// recentDays is the window of "recently" and "lately": the last 30 days through the end of today.
const recentDays = 30

// units matches the calendar units of relative expressions.
const units = `(day|week|month|year|decade)s?`

// relativeRules are expressions relative to ref: today, yesterday, N units ago, last N units, this/last/next
// week|month|year|quarter|decade|weekend, earlier/later this year, recently.
var relativeRules = []rule{
	r(`\b(earlier|later)\s+(?:today|this\s+(week|month|year))\b`, resolveEarlierLater),
	r(`\b(`+counts+`)\s+(?:of\s+)?`+units+`\s+(?:ago|back)\b`, resolveAgo),
	r(`\b(?:last|past|previous|prior)\s+(`+counts+`)\s+(?:of\s+)?`+units+`\b`, resolveLastN),
	r(`\b(?:next|coming|following)\s+(`+counts+`)\s+(?:of\s+)?`+units+`\b`, resolveNextN),
	r(`\b(?:today|tonight|this\s+(?:morning|afternoon|evening))\b`, fixedDay(0)),
	r(`\b(?:yesterday|last\s+night)\b`, fixedDay(-1)),
	r(`\btomorrow\b`, fixedDay(1)),
	r(`\b(this|last|next|past|previous|coming)\s+weekend\b`, resolveWeekend),
	r(`\b(?:over|on|during)\s+the\s+weekend\b`, resolveWeekend),
	r(`\b(this|last|next|previous|past|coming|current)\s+(week|month|year|quarter|decade)\b`, resolveUnit),
	r(`\b(?:recently|lately)\b`, func(_ match, ref time.Time) (domain.TimeWindow, bool) {
		today := startOfDay(ref)
		return domain.TimeWindow{Start: addDays(today, -recentDays), End: addDays(today, 1)}, true
	}),
}

// resolveEarlierLater splits today or the current week/month/year at ref.
func resolveEarlierLater(m match, ref time.Time) (domain.TimeWindow, bool) {
	unit := m.g(2)
	if unit == "" {
		unit = "day"
	}
	w := calendarUnit(ref, unit, 0)
	if m.g(1) == "earlier" {
		return domain.TimeWindow{Start: w.Start, End: ref}, true
	}
	return domain.TimeWindow{Start: ref, End: w.End}, true
}

// resolveAgo is the calendar unit N units before ref's ("3 days ago" is that day, "2 weeks ago" that
// Monday-start week). A vague count (couple, few, several) widens to the units N-1 through N+1 back.
func resolveAgo(m match, ref time.Time) (domain.TimeWindow, bool) {
	n, vague, ok := parseCount(m.g(1))
	unit := m.g(2)
	if !ok {
		return domain.TimeWindow{}, false
	}
	if !vague {
		return calendarUnit(ref, unit, -n), true
	}
	return domain.TimeWindow{Start: calendarUnit(ref, unit, -n-1).Start, End: calendarUnit(ref, unit, -n+1).End}, true
}

// resolveLastN is day-aligned and rolling: from midnight N units before ref's day through the end of today,
// so "last 7 days" on the 10th is [the 3rd, the 11th).
func resolveLastN(m match, ref time.Time) (domain.TimeWindow, bool) {
	n, _, ok := parseCount(m.g(1))
	if !ok {
		return domain.TimeWindow{}, false
	}
	today := startOfDay(ref)
	return domain.TimeWindow{Start: unitShift(today, m.g(2), -n), End: addDays(today, 1)}, true
}

// resolveNextN mirrors resolveLastN forwards: from today's midnight through N units after the end of today.
func resolveNextN(m match, ref time.Time) (domain.TimeWindow, bool) {
	n, _, ok := parseCount(m.g(1))
	if !ok {
		return domain.TimeWindow{}, false
	}
	today := startOfDay(ref)
	return domain.TimeWindow{Start: today, End: unitShift(addDays(today, 1), m.g(2), n)}, true
}

// resolveWeekend is Saturday and Sunday of the current week ("this"), the previous ("last", "past") or the
// following one ("next"); "over the weekend" is the current weekend on a weekend day, else the last one.
func resolveWeekend(m match, ref time.Time) (domain.TimeWindow, bool) {
	sat := addDays(weekStart(ref), 5)
	switch m.g(1) {
	case "last", "past", "previous":
		sat = addDays(sat, -7)
	case "next", "coming":
		sat = addDays(sat, 7)
	case "this":
	default:
		if wd := ref.Weekday(); wd != time.Saturday && wd != time.Sunday {
			sat = addDays(sat, -7)
		}
	}
	return domain.TimeWindow{Start: sat, End: addDays(sat, 2)}, true
}

// resolveUnit is the calendar unit this/last/next to ref's; "past" is rolling instead (the past month runs
// from the same day a month ago through the end of today).
func resolveUnit(m match, ref time.Time) (domain.TimeWindow, bool) {
	unit := m.g(2)
	switch m.g(1) {
	case "last", "previous":
		return calendarUnit(ref, unit, -1), true
	case "next", "coming":
		return calendarUnit(ref, unit, 1), true
	case "past":
		today := startOfDay(ref)
		start := unitShift(today, unit, -1)
		if unit == "quarter" {
			start = unitShift(today, "month", -3)
		}
		return domain.TimeWindow{Start: start, End: addDays(today, 1)}, true
	default:
		return calendarUnit(ref, unit, 0), true
	}
}
