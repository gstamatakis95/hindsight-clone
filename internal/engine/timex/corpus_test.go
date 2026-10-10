package timex

import (
	"memd/internal/domain"
	"time"
)

// testRef is Saturday 2026-10-10 15:04:05 UTC, the corpus reference time.
var testRef = time.Date(2026, 10, 10, 15, 4, 5, 0, time.UTC)

// jst and est are fixed zones for the non-UTC rows (no tzdata dependency).
var (
	jst = time.FixedZone("JST", 9*3600)
	est = time.FixedZone("EST", -5*3600)
)

// Non-UTC references: 02:00 JST on Saturday the 10th is still Friday the 9th in UTC; 01:00 EST on
// Thursday 1 October is already 06:00 UTC.
var (
	jstRef = time.Date(2026, 10, 10, 2, 0, 0, 0, jst)
	estRef = time.Date(2026, 10, 1, 1, 0, 0, 0, est)
)

// z is an unbounded window bound.
var z time.Time

// d is UTC midnight of y-m-dd.
func d(y int, m time.Month, dd int) time.Time {
	return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
}

// dz is midnight of y-m-dd in loc.
func dz(loc *time.Location, y int, m time.Month, dd int) time.Time {
	return time.Date(y, m, dd, 0, 0, 0, 0, loc)
}

// w is the window [s, e).
func w(s, e time.Time) *domain.TimeWindow {
	return &domain.TimeWindow{Start: s, End: e}
}

// corpusRow is one query with its expected window; a nil want means no window. ref zero means testRef.
type corpusRow struct {
	cat   string
	query string
	want  *domain.TimeWindow
	ref   time.Time
}

// relativeRows are expressions relative to the reference day.
var relativeRows = []corpusRow{
	{"relative", "what did I do today?", w(d(2026, 10, 10), d(2026, 10, 11)), z},
	{"relative", "anything planned for tonight", w(d(2026, 10, 10), d(2026, 10, 11)), z},
	{"relative", "what did I eat yesterday", w(d(2026, 10, 9), d(2026, 10, 10)), z},
	{"relative", "what's on tomorrow", w(d(2026, 10, 11), d(2026, 10, 12)), z},
	{"relative", "the day before yesterday", w(d(2026, 10, 8), d(2026, 10, 9)), z},
	{"relative", "plans for the day after tomorrow", w(d(2026, 10, 12), d(2026, 10, 13)), z},
	{"relative", "what happened this morning", w(d(2026, 10, 10), d(2026, 10, 11)), z},
	{"relative", "who called last night?", w(d(2026, 10, 9), d(2026, 10, 10)), z},
	{"relative", "the bug I fixed 3 days ago", w(d(2026, 10, 7), d(2026, 10, 8)), z},
	{"relative", "two weeks ago", w(d(2026, 9, 21), d(2026, 9, 28)), z},
	{"relative", "a month ago", w(d(2026, 9, 1), d(2026, 10, 1)), z},
	{"relative", "5 years ago", w(d(2021, 1, 1), d(2022, 1, 1)), z},
	{"relative", "a couple of weeks back", w(d(2026, 9, 14), d(2026, 10, 5)), z},
	{"relative", "a few days ago", w(d(2026, 10, 6), d(2026, 10, 9)), z},
	{"relative", "commits in the last 7 days", w(d(2026, 10, 3), d(2026, 10, 11)), z},
	{"relative", "in the past 30 days", w(d(2026, 9, 10), d(2026, 10, 11)), z},
	{"relative", "over the last 3 months", w(d(2026, 7, 10), d(2026, 10, 11)), z},
	{"relative", "the previous two weeks", w(d(2026, 9, 26), d(2026, 10, 11)), z},
	{"relative", "the past few years", w(d(2023, 10, 10), d(2026, 10, 11)), z},
	{"relative", "the next 3 days", w(d(2026, 10, 10), d(2026, 10, 14)), z},
	{"relative", "meetings this week", w(d(2026, 10, 5), d(2026, 10, 12)), z},
	{"relative", "what did we decide last week?", w(d(2026, 9, 28), d(2026, 10, 5)), z},
	{"relative", "next week's agenda", w(d(2026, 10, 12), d(2026, 10, 19)), z},
	{"relative", "this month", w(d(2026, 10, 1), d(2026, 11, 1)), z},
	{"relative", "expenses from last month", w(d(2026, 9, 1), d(2026, 10, 1)), z},
	{"relative", "next month", w(d(2026, 11, 1), d(2026, 12, 1)), z},
	{"relative", "goals for this year", w(d(2026, 1, 1), d(2027, 1, 1)), z},
	{"relative", "where did I travel last year", w(d(2025, 1, 1), d(2026, 1, 1)), z},
	{"relative", "next year", w(d(2027, 1, 1), d(2028, 1, 1)), z},
	{"relative", "this weekend", w(d(2026, 10, 10), d(2026, 10, 12)), z},
	{"relative", "what did I do last weekend", w(d(2026, 10, 3), d(2026, 10, 5)), z},
	{"relative", "next weekend", w(d(2026, 10, 17), d(2026, 10, 19)), z},
	{"relative", "in the past week", w(d(2026, 10, 3), d(2026, 10, 11)), z},
	{"relative", "over the weekend", w(d(2026, 10, 10), d(2026, 10, 12)), z},
	{"relative", "earlier this year", w(d(2026, 1, 1), testRef), z},
	{"relative", "later this month", w(testRef, d(2026, 11, 1)), z},
	{"relative", "what have I read recently", w(d(2026, 9, 10), d(2026, 10, 11)), z},
	{"relative", "lately", w(d(2026, 9, 10), d(2026, 10, 11)), z},
	{"relative", "last quarter", w(d(2026, 7, 1), d(2026, 10, 1)), z},
	{"relative", "this quarter", w(d(2026, 10, 1), d(2027, 1, 1)), z},
	{"relative", "the last decade", w(d(2010, 1, 1), d(2020, 1, 1)), z},
	{"relative", "999 decades ago", nil, z}, // before year 1: consumed by the rules as no constraint
}

// namedRows are weekdays, seasons, months and quarters without a year.
var namedRows = []corpusRow{
	{"named", "last Tuesday", w(d(2026, 10, 6), d(2026, 10, 7)), z},
	{"named", "this Tuesday", w(d(2026, 10, 6), d(2026, 10, 7)), z},
	{"named", "next Tuesday", w(d(2026, 10, 13), d(2026, 10, 14)), z},
	{"named", "what did I cook on Saturday", w(d(2026, 10, 10), d(2026, 10, 11)), z},
	{"named", "last Saturday", w(d(2026, 10, 3), d(2026, 10, 4)), z},
	{"named", "friday night", w(d(2026, 10, 9), d(2026, 10, 10)), z},
	{"named", "this Sunday", w(d(2026, 10, 11), d(2026, 10, 12)), z},
	{"named", "last summer", w(d(2026, 6, 1), d(2026, 9, 1)), z},
	{"named", "the trip last fall", w(d(2025, 9, 1), d(2025, 12, 1)), z},
	{"named", "this spring", w(d(2026, 3, 1), d(2026, 6, 1)), z},
	{"named", "next winter", w(d(2026, 12, 1), d(2027, 3, 1)), z},
	{"named", "last winter", w(d(2025, 12, 1), d(2026, 3, 1)), z},
	{"named", "in the autumn", w(d(2026, 9, 1), d(2026, 12, 1)), z},
	{"named", "what did we do in March?", w(d(2026, 3, 1), d(2026, 4, 1)), z},
	{"named", "in November", w(d(2025, 11, 1), d(2025, 12, 1)), z},
	{"named", "last October", w(d(2025, 10, 1), d(2025, 11, 1)), z},
	{"named", "this March", w(d(2026, 3, 1), d(2026, 4, 1)), z},
	{"named", "next March", w(d(2027, 3, 1), d(2027, 4, 1)), z},
	{"named", "during May", w(d(2026, 5, 1), d(2026, 6, 1)), z},
	{"named", "on March 5th", w(d(2026, 3, 5), d(2026, 3, 6)), z},
	{"named", "the 4th of July", w(d(2026, 7, 4), d(2026, 7, 5)), z},
	{"named", "Q1", w(d(2026, 1, 1), d(2026, 4, 1)), z},
}

// absoluteRows are dates, months, seasons, quarters, years and decades pinned to a year.
var absoluteRows = []corpusRow{
	{"absolute", "2024-03-05", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "what happened on March 5, 2024?", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "5 March 2024", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "03/05/2024", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "25/12/2023", w(d(2023, 12, 25), d(2023, 12, 26)), z},
	{"absolute", "2024-03", w(d(2024, 3, 1), d(2024, 4, 1)), z},
	{"absolute", "March 2024", w(d(2024, 3, 1), d(2024, 4, 1)), z},
	{"absolute", "summer of 2024", w(d(2024, 6, 1), d(2024, 9, 1)), z},
	{"absolute", "what did we ship in 2023", w(d(2023, 1, 1), d(2024, 1, 1)), z},
	{"absolute", "2019 vacation photos", w(d(2019, 1, 1), d(2020, 1, 1)), z},
	{"absolute", "Q3 2024", w(d(2024, 7, 1), d(2024, 10, 1)), z},
	{"absolute", "the second quarter of 2025", w(d(2025, 4, 1), d(2025, 7, 1)), z},
	{"absolute", "music from the 90s", w(d(1990, 1, 1), d(2000, 1, 1)), z},
	{"absolute", "in the 1990s", w(d(1990, 1, 1), d(2000, 1, 1)), z},
	{"absolute", "the '20s", w(d(2020, 1, 1), d(2030, 1, 1)), z},
	{"absolute", "March 3-5, 2024", w(d(2024, 3, 3), d(2024, 3, 6)), z},
	{"absolute", "March 3 - 5 2024", w(d(2024, 3, 3), d(2024, 3, 6)), z},
	{"absolute", "3/5/99", w(d(1999, 3, 5), d(1999, 3, 6)), z},
	{"absolute", "3/5/24", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "summer of '98", w(d(1998, 6, 1), d(1998, 9, 1)), z},
	{"absolute", "2024-03-05T14:30:00Z", w(d(2024, 3, 5), d(2024, 3, 6)), z},
	{"absolute", "2024-03-05 14:30", w(d(2024, 3, 5), d(2024, 3, 6)), z},
}

// rangeRows are bounded and open ranges.
var rangeRows = []corpusRow{
	{"range", "between March and May 2024", w(d(2024, 3, 1), d(2024, 6, 1)), z},
	{"range", "from 2020 to 2022", w(d(2020, 1, 1), d(2023, 1, 1)), z},
	{"range", "since 2021", w(d(2021, 1, 1), testRef), z},
	{"range", "everything since last March", w(d(2026, 3, 1), testRef), z},
	{"range", "before 2020", w(z, d(2020, 1, 1)), z},
	{"range", "after June 2023", w(d(2023, 7, 1), z), z},
	{"range", "until last week", w(z, d(2026, 10, 5)), z},
	{"range", "2018-2020", w(d(2018, 1, 1), d(2021, 1, 1)), z},
	{"range", "from March to May", w(d(2026, 3, 1), d(2026, 6, 1)), z},
	{"range", "between Monday and Wednesday", w(d(2026, 10, 5), d(2026, 10, 8)), z},
	{"range", "since yesterday", w(d(2026, 10, 9), testRef), z},
	{"range", "from 2020 to now", w(d(2020, 1, 1), testRef), z},
	{"range", "since 2021 until today", w(d(2021, 1, 1), d(2026, 10, 11)), z},
	{"range", "between March and present", w(d(2026, 3, 1), testRef), z},
}

// zoneRows use a non-UTC reference: boundaries follow the reference's location.
var zoneRows = []corpusRow{
	{"zone", "yesterday", w(dz(jst, 2026, 10, 9), dz(jst, 2026, 10, 10)), jstRef},
	{"zone", "this week", w(dz(jst, 2026, 10, 5), dz(jst, 2026, 10, 12)), jstRef},
	{"zone", "last month", w(dz(est, 2026, 9, 1), dz(est, 2026, 10, 1)), estRef},
	{"zone", "today", w(dz(est, 2026, 10, 1), dz(est, 2026, 10, 2)), estRef},
}

// noneRows carry no time constraint, including the lexical traps.
var noneRows = []corpusRow{
	{"none", "what is my name", nil, z},
	{"none", "may I ask you something?", nil, z},
	{"none", "march on, soldiers", nil, z},
	{"none", "I need a second opinion", nil, z},
	{"none", "where can I buy spring water", nil, z},
	{"none", "I didn't fall down the stairs", nil, z},
	{"none", "best Sunday roast recipe", nil, z},
	{"none", "now that you mention it, what's my favorite color", nil, z},
	{"none", "I have 3 cats", nil, z},
	{"none", "the date field in the form", nil, z},
	{"none", "since you asked, what is my name", nil, z},
	{"none", "a message from Bob to Alice", nil, z},
	{"none", "what were you up to", nil, z},
	{"none", "is port 8080 open", nil, z},
	{"none", "ticket 2019 is still open", nil, z},
	{"none", "I paid $2000 for it", nil, z},
	{"none", "this may be wrong", nil, z},
	{"none", "we fall in love easily", nil, z},
	{"none", "the march in Washington", nil, z},
	{"none", "last but not least, my dog", nil, z},
	{"none", "remind me to run tests before I deploy", nil, z},
	{"none", "what do I eat after my workout", nil, z},
	{"none", "my overnight oats recipe", nil, z},
	{"none", "any weekend plans?", nil, z},
	{"none", "", nil, z},
}

// corpus is every query-window row.
func corpus() []corpusRow {
	var all []corpusRow
	for _, rows := range [][]corpusRow{relativeRows, namedRows, absoluteRows, rangeRows, zoneRows, noneRows} {
		all = append(all, rows...)
	}
	return all
}

// vagueCues are temporal cues the rules leave to the TIME_PARSE fallback.
var vagueCues = []string{
	"the day before my birthday",
	"around the holidays",
	"when I was in college",
	"the other day",
	"back in college",
	"what happened after that",
	"since then",
	"at the time",
	"during the conference",
}
