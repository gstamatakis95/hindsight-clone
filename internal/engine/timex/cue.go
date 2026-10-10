package timex

import (
	"regexp"
	"strconv"
)

// The temporal-cue gate. It is lexical and cheap, and decides whether a query may carry a time constraint
// at all: everything the rules resolve is a cue, plus vaguer phrasing the rules leave to the TIME_PARSE
// fallback ("the other day", "around the holidays", "when I was in college"). Ambiguous words count only
// in a temporal frame: a month name next to a day, a year or a preposition/determiner ("in March", not
// "march on" or "may I ask"); a season or weekday after a determiner ("last fall", not "fall down",
// "spring water" or "Sunday roast"); a four-digit number only when it reads as a year (not "port 2019",
// "$2000"). "now" and "date" alone are not cues.

// cueYearMin and cueYearMax bound a bare four-digit number counted as a year (wider than the rules' bound).
const (
	cueYearMin = 1800
	cueYearMax = 2199
)

// cuePatterns are the lexical cues, on normalized text.
var cuePatterns = compileAll(
	`\b(?:today|tonight|yesterday|tomorrow|ago|recently|lately|nowadays|weekends?|overnight)\b`,
	`\b(?:last|this|next|past|previous|coming|upcoming|following|prior|current)\s+(?:[a-z0-9']+\s+){0,3}?`+
		`(?:hours?|days?|nights?|weeks?|months?|years?|quarters?|decades?|semesters?|mornings?|`+
		`afternoons?|evenings?)\b`,
	`\b(?:`+counts+`|some|many)\s+(?:of\s+)?(?:hours?|days?|weeks?|months?|years?|decades?)\s+`+
		`(?:ago|back|before|after|later|earlier|prior)\b`,
	`\b(?:last|this|next|past|previous|coming|on|since|until|till|before|after|every|by|from|between)\s+`+
		`(?:`+weekdays+`)s?\b`,
	`\b(?:`+weekdays+`)s?\s+(?:morning|afternoon|evening|night)\b`,
	`\b(?:`+seasonPreceders+`|since|until|till|before|after|every|of|by|from|between)\s+(?:the\s+)?`+
		`(?:`+seasons+`)\b`,
	`\b(?:`+seasons+`)\s+(?:of\s+)?\d{4}\b`,
	`\b(?:`+monthAny+`)\s+\d{1,2}`+ordinal+`\b`,
	`\b\d{1,2}`+ordinal+`\s+(?:of\s+)?(?:`+monthAny+`)\b`,
	`\b(?:`+monthAny+`)\s+(?:of\s+)?\d{4}\b`,
	`\b\d{4}[-/]\d{1,2}(?:[-/]\d{1,2})?\b`,
	`\b\d{1,2}/\d{1,2}/(?:\d{4}|\d{2})\b`,
	`\b\d{4}\s*-\s*\d{4}\b`,
	`\b\d{3}0'?s\b`,
	`\bthe\s+'?\d0'?s\b`,
	`\bq[1-4]\b`,
	`\b(?:first|second|third|fourth|1st|2nd|3rd|4th|last|this|next|previous|past)\s+quarter\b`,
	`\bthe\s+other\s+(?:day|night|week|month)\b`,
	`\b(?:day|night|week|weekend|month|year|morning|evening)\s+(?:before|after)\b`,
	`\b(?:when|while)\s+(?:i|we|you|he|she|they|it)\s+(?:was|were|lived|worked|studied|moved|started|left|`+
		`got|had)\b`,
	`\bduring\b`,
	`\b(?:around|over|since|until|till|before|after|through)\s+(?:the\s+)?(?:holidays?|christmas|thanksgiving|`+
		`easter|halloween|new\s+year'?s?|vacation|summer\s+break|spring\s+break|break|semester|wedding|`+
		`birthday|graduation)\b`,
	`\b(?:until|till|before|after)\s+(?:then|that|my|our|his|her|their|we|i|he|she|they)\b`,
	`\bsince\s+(?:then|my|our|his|her|their)\b`,
	`\bback\s+(?:then|when|in\s+(?:the\s+day|\d{4}|college|school|high\s+school|university))\b`,
	`\b(?:at\s+the\s+time|in\s+the\s+past|a\s+while\s+back|a\s+bit\s+back)\b`,
	`\b(?:earlier|later)\s+(?:today|this|that|in\s+the)\b`,
)

// monthCue matches a month name after a preposition or determiner; resolveMonthCue drops "this may".
var monthCue = regexp.MustCompile(`\b(` + monthPreceders + `|since|until|till|before|after|by|from|between|` +
	`through)\s+(?:the\s+month\s+of\s+)?(` + monthFull + `)\b`)

// yearCue matches a four-digit number; plausibleYearToken decides whether it is a year.
var yearCue = regexp.MustCompile(`\b(\d{4})\b`)

// hasTemporalCue is the gate for the LLM fallback: false means the query carries no time constraint.
func hasTemporalCue(query string) bool {
	s := normalize(query)
	if s == "" {
		return false
	}
	for _, re := range cuePatterns {
		if re.MatchString(s) {
			return true
		}
	}
	for _, loc := range monthCue.FindAllStringSubmatchIndex(s, -1) {
		if s[loc[2]:loc[3]] != "this" || s[loc[4]:loc[5]] != "may" {
			return true
		}
	}
	for _, loc := range yearCue.FindAllStringSubmatchIndex(s, -1) {
		y, err := strconv.Atoi(s[loc[2]:loc[3]])
		if err == nil && y >= cueYearMin && y <= cueYearMax && plausibleYearToken(s, loc[2], loc[3]) {
			return true
		}
	}
	return false
}

// compileAll compiles cue patterns.
func compileAll(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}
