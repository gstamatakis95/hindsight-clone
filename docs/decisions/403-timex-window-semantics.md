# 403 — timex window semantics and cue rules

## Context
The spec fixes the shape of timex: rules run first, `hasTemporalCue` gates the TIME_PARSE fallback, and
no cue means no window and no call. It does not define what each expression means as a window.
Hindsight relies on `dateparser` plus its own period tables, and Go has no equivalent library on the
allowed list. So the rules are hand-written in the standard library, and their readings need to be
written down.

## Decision

### Frame
- All arithmetic uses `ref` in its own location.
- Weeks start on Monday.
- Every window is half-open, [Start, End).
- A zero `ref` falls back to the injected `Clock`.
- At retain, `OccurredStart` and `OccurredEnd` take the window bounds, so `OccurredEnd` is the
  exclusive end. "Yesterday" is [Fri 00:00, Sat 00:00). An unbounded side stays nil.

### Relative expressions
- **"last/past/previous N days|weeks|months|years"** runs from midnight N units back through the end of
  today. "Next N" mirrors it forward.
- **"last/this/next week|month|year|quarter|decade"** is the calendar period.
- **"past week/month/year"** with no number is rolling.
- **"N units ago"** is that whole calendar unit.
- **Vague counts:** couple = 2; few and several = 3. They widen to N−1..N+1 units.
- **"recently" and "lately"** are the last 30 days.
- **"earlier this year"** is [Jan 1, ref).
- **Weekdays:**
  - "last X" is strictly before today.
  - "this X" falls in the current week.
  - "next X" is strictly after today.
  - "on X" includes today.

### Named periods without a year
- The answer is the most recent occurrence starting no later than `ref`.
- Seasons are northern-hemisphere meteorological: spring is Mar–May, summer Jun–Aug, fall Sep–Nov, and
  winter Y is Dec Y–Feb Y+1.
- Month and year shifts clamp to the last day of the month.

### Absolute dates
- Accepted forms: ISO, "March 5, 2024", "5 March 2024", "the 4th of July".
- Slash dates are US m/d, swapped to d/m when the first number is over 12.
- A two-digit year means 20yy.
- A bare 4-digit year counts only within ref−120..ref+20 years, and never next to `$ # / : -` or after an
  identifier word (port, ticket, version, page, …).

### Ranges
- **"between X and Y" / "from X to Y"** is [X.Start, Y.End). X borrows Y's year.
- **"since X"** is [X.Start, ref).
- **"until X"** is [∞, X.End).
- **"before X"** is [∞, X.Start).
- **"after X"** is [X.End, ∞).

### Cues
A temporal cue is a word that can only be about time (yesterday, ago, recently, …), a parseable date,
or a qualified period.
- **Months** count only next to a day, a year, or a preposition/determiner ("in March", "last May").
  "may I", "march on" and "this may" are not cues.
- **Seasons and weekdays** need a qualifier before them, so "spring water", "fall down" and
  "Sunday roast" are not cues.
- **"since"** counts only before then/my/our/his/her/their.
- **"now", "date" and "second"** on their own are not cues.
- **Vague phrases go to the fallback:** "the day before my birthday", "around the holidays",
  "when I was in college", "the other day", "since then", and similar.

### Fallback
- `Structured` uses template `time_parse_system` with its schema, and returns `{start, end}`.
- Both bounds null, start ≥ end, or an unparseable bound gives a nil window, not an error. A one-sided
  answer is kept.
- Router and LLM errors are wrapped and returned. At retain that fails the job, which retries, rather
  than storing a wrong date.

## Known false positives (accepted)
- "I have 2000 cats" reads as the year 2000.
- "this spring water" reads as a season.

Recall only uses the window for the temporal arm and the temporal boost, so a stray window narrows one
arm. It does not filter results.

## Alternatives
- A date library (dateparser-style). It is not on the dependency list.
- Always calling TIME_PARSE. The spec forbids it.

## Spec lines involved
Model routing (TIME_PARSE row), Engines (TimeNormalizer), Function signatures (`parseRules`,
`hasTemporalCue`), Recall pipeline (temporal arm).
