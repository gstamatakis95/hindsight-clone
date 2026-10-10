package timex

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Shared vocabulary of the rules and the cue gate (English only; multilingual input is out of scope).

const (
	// monthFull matches full month names.
	monthFull = `january|february|march|april|may|june|july|august|september|october|november|december`
	// monthAny matches full and abbreviated month names (abbreviations only appear next to a day or year).
	monthAny = monthFull + `|jan|feb|mar|apr|jun|jul|aug|sept|sep|oct|nov|dec`
	// weekdays matches weekday names.
	weekdays = `monday|tuesday|wednesday|thursday|friday|saturday|sunday`
	// seasons matches season names.
	seasons = `spring|summer|fall|autumn|winter`
	// counts matches a count in digits or words; vague counts (couple, few, several) widen the window.
	counts = `\d{1,3}|an?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|` +
		`a\s+couple|couple|a\s+few|few|several`
	// ordinal is an optional English ordinal suffix.
	ordinal = `(?:st|nd|rd|th)?`
)

// monthNames maps every month spelling in monthAny to its month.
var monthNames = map[string]time.Month{
	"january": time.January, "jan": time.January, "february": time.February, "feb": time.February,
	"march": time.March, "mar": time.March, "april": time.April, "apr": time.April, "may": time.May,
	"june": time.June, "jun": time.June, "july": time.July, "jul": time.July, "august": time.August,
	"aug": time.August, "september": time.September, "sept": time.September, "sep": time.September,
	"october": time.October, "oct": time.October, "november": time.November, "nov": time.November,
	"december": time.December, "dec": time.December,
}

// weekdayNames maps weekday names to their weekday.
var weekdayNames = map[string]time.Weekday{
	"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday, "sunday": time.Sunday,
}

// seasonStart maps a season to its first month.
var seasonStart = map[string]time.Month{
	"spring": time.March, "summer": time.June, "fall": time.September, "autumn": time.September,
	"winter": time.December,
}

// countWords maps word counts to numbers; vague marks the approximate ones.
var countWords = map[string]struct {
	n     int
	vague bool
}{
	"a": {1, false}, "an": {1, false}, "one": {1, false}, "two": {2, false}, "three": {3, false},
	"four": {4, false}, "five": {5, false}, "six": {6, false}, "seven": {7, false}, "eight": {8, false},
	"nine": {9, false}, "ten": {10, false}, "eleven": {11, false}, "twelve": {12, false},
	"couple": {2, true}, "a couple": {2, true}, "few": {3, true}, "a few": {3, true}, "several": {3, true},
}

// ordinalWords maps quarter ordinals to numbers.
var ordinalWords = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "1st": 1, "2nd": 2, "3rd": 3, "4th": 4,
}

// parseCount reads a count token; ok is false for an unknown or zero count.
func parseCount(s string) (n int, vague, ok bool) {
	s = strings.Join(strings.Fields(s), " ")
	if c, found := countWords[s]; found {
		return c.n, c.vague, true
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return 0, false, false
	}
	return v, false, true
}

// normalize lowercases text and reduces it to space-separated tokens. Letters, digits, apostrophes, '$',
// '#', '%', '/' and ':' survive; a hyphen survives only next to a digit ("2024-03-05", "2020 - 2022") so
// "mid-march" reads as "mid march"; everything else becomes a space.
func normalize(text string) string {
	rs := []rune(strings.ToLower(text))
	var b strings.Builder
	b.Grow(len(rs))
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '’':
			b.WriteRune('\'')
		case strings.ContainsRune("$#%/:", r):
			b.WriteRune(r)
		case r == '-' && nearDigit(rs, i):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// nearDigit reports whether the closest non-space rune on either side of rs[i] is a digit.
func nearDigit(rs []rune, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if rs[j] != ' ' {
			if unicode.IsDigit(rs[j]) {
				return true
			}
			break
		}
	}
	for j := i + 1; j < len(rs); j++ {
		if rs[j] != ' ' {
			return unicode.IsDigit(rs[j])
		}
	}
	return false
}

// nonYearWords precede a four-digit number that is an identifier, not a year ("port 2019", "ticket 2020").
var nonYearWords = map[string]bool{
	"port": true, "ticket": true, "issue": true, "pr": true, "id": true, "number": true, "no": true,
	"version": true, "v": true, "page": true, "room": true, "line": true, "step": true, "item": true,
	"order": true, "code": true, "pin": true, "error": true, "build": true, "invoice": true, "account": true,
	"zip": true, "ext": true, "extension": true, "model": true, "flight": true, "route": true, "bus": true,
}

// plausibleYearToken reports whether the four-digit token at s[i:j] reads as a year rather than an
// amount, identifier or part of a larger number: it is not glued to '$', '#', '%', '/', ':' or '-', and
// the word before it is not an identifier word such as "port" or "ticket".
func plausibleYearToken(s string, i, j int) bool {
	if i > 0 && strings.ContainsRune("$#/:-", rune(s[i-1])) {
		return false
	}
	if j < len(s) && strings.ContainsRune("%/:-", rune(s[j])) {
		return false
	}
	prev := strings.Fields(s[:i])
	return len(prev) == 0 || !nonYearWords[prev[len(prev)-1]]
}
