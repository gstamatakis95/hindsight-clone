package timex

import (
	"memd/internal/domain"
	"time"
)

// Calendar helpers. Every boundary is built with time.Date in ref.Location(), so day arithmetic follows the
// wall clock (DST-safe) and weeks start on Monday.

// day returns local midnight of y-m-d in loc; out-of-range values normalize like time.Date.
func day(y int, m time.Month, d int, loc *time.Location) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// startOfDay is local midnight of t's day.
func startOfDay(t time.Time) time.Time {
	return day(t.Year(), t.Month(), t.Day(), t.Location())
}

// addDays moves a midnight by n calendar days.
func addDays(t time.Time, n int) time.Time {
	return day(t.Year(), t.Month(), t.Day()+n, t.Location())
}

// dayWindow is the single day starting at midnight t.
func dayWindow(t time.Time) domain.TimeWindow {
	return domain.TimeWindow{Start: t, End: addDays(t, 1)}
}

// weekStart is the Monday midnight of t's week.
func weekStart(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7
	return addDays(startOfDay(t), -offset)
}

// monthWindow is calendar month m of year y.
func monthWindow(y int, m time.Month, loc *time.Location) domain.TimeWindow {
	return domain.TimeWindow{Start: day(y, m, 1, loc), End: day(y, m+1, 1, loc)}
}

// yearWindow is calendar year y.
func yearWindow(y int, loc *time.Location) domain.TimeWindow {
	return domain.TimeWindow{Start: day(y, 1, 1, loc), End: day(y+1, 1, 1, loc)}
}

// quarterWindow is quarter q (1-4) of year y.
func quarterWindow(y, q int, loc *time.Location) domain.TimeWindow {
	m := time.Month(3*(q-1) + 1)
	return domain.TimeWindow{Start: day(y, m, 1, loc), End: day(y, m+3, 1, loc)}
}

// quarterOf is the 1-based quarter of t.
func quarterOf(t time.Time) int {
	return (int(t.Month())-1)/3 + 1
}

// decadeWindow is the decade starting at year start (a multiple of ten).
func decadeWindow(start int, loc *time.Location) domain.TimeWindow {
	return domain.TimeWindow{Start: day(start, 1, 1, loc), End: day(start+10, 1, 1, loc)}
}

// seasonWindow is one occurrence of a meteorological (northern hemisphere) season: spring is March-May,
// summer June-August, fall September-November, and winter y runs from December y to the end of February y+1.
func seasonWindow(first time.Month, y int, loc *time.Location) domain.TimeWindow {
	return domain.TimeWindow{Start: day(y, first, 1, loc), End: day(y, first+3, 1, loc)}
}

// unitShift moves midnight t by n units (day, week, month, year, decade). Month and year shifts clamp the
// day to the target month's last day, so a month before March 31 is February 28 (not March 3).
func unitShift(t time.Time, unit string, n int) time.Time {
	switch unit {
	case "week":
		return addDays(t, 7*n)
	case "month":
		return clampedDay(t.Year(), t.Month()+time.Month(n), t.Day(), t.Location())
	case "year":
		return clampedDay(t.Year()+n, t.Month(), t.Day(), t.Location())
	case "decade":
		return clampedDay(t.Year()+10*n, t.Month(), t.Day(), t.Location())
	default:
		return addDays(t, n)
	}
}

// clampedDay is y-m-d with d capped at the month's last day (m may be out of range, as in time.Date).
func clampedDay(y int, m time.Month, d int, loc *time.Location) time.Time {
	first := day(y, m, 1, loc)
	if last := day(first.Year(), first.Month()+1, 0, loc).Day(); d > last {
		d = last
	}
	return day(first.Year(), first.Month(), d, loc)
}

// unitStart is the start of the calendar unit containing ref.
func unitStart(ref time.Time, unit string) time.Time {
	loc := ref.Location()
	switch unit {
	case "week":
		return weekStart(ref)
	case "month":
		return day(ref.Year(), ref.Month(), 1, loc)
	case "quarter":
		return day(ref.Year(), time.Month(3*(quarterOf(ref)-1)+1), 1, loc)
	case "year":
		return day(ref.Year(), 1, 1, loc)
	case "decade":
		return day(ref.Year()-ref.Year()%10, 1, 1, loc)
	default:
		return startOfDay(ref)
	}
}

// calendarUnit is the calendar unit containing ref shifted by n units (n=-1 is "last week").
func calendarUnit(ref time.Time, unit string, n int) domain.TimeWindow {
	if unit == "quarter" {
		start := unitStart(ref, unit)
		s := day(start.Year(), start.Month()+time.Month(3*n), 1, start.Location())
		return domain.TimeWindow{Start: s, End: day(s.Year(), s.Month()+3, 1, s.Location())}
	}
	s := unitShift(unitStart(ref, unit), unit, n)
	return domain.TimeWindow{Start: s, End: unitShift(s, unit, 1)}
}

// Supported years: a window with a bound outside them means no constraint rather than an error.
const (
	minYear = 1
	maxYear = 9999
)

// inYearRange reports whether every bound of w (an unbounded zero side aside) falls in years 1-9999.
func inYearRange(w domain.TimeWindow) bool {
	for _, t := range []time.Time{w.Start, w.End} {
		if !t.IsZero() && (t.Year() < minYear || t.Year() > maxYear) {
			return false
		}
	}
	return true
}

// validDate reports whether y-m-d is a real calendar date.
func validDate(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Day() == d && int(t.Month()) == m
}
