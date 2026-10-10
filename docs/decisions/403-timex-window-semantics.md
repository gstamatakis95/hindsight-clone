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

### Fallback and degradation (review round 1, after Hindsight temporal_extraction.py:99-115)
- `Structured` uses template `time_parse_system` with its schema and returns `{start, end}`.
- Both bounds null, start >= end, or an unparseable bound gives a nil window, not an error. A one-sided
  answer is kept.
- **Year range.** A window with any bound outside years 1..9999 is no window, from the rules or from the
  LLM. The rules consume it, so no fallback call happens: "999 decades ago" gives nil, and so does
  "December 9999", whose exclusive end is year 10000.
- **QueryWindow degrades.** Any fallback error (router, LLM, decode, gateway timeout) is logged with
  `slog` and gives `(nil, nil)`: recall runs without a temporal constraint. Only the caller's own
  cancellation (`ctx.Err() != nil` or `context.Canceled`) is returned. A gateway per-call
  `DeadlineExceeded` wrapped in `ErrUnavailable` still degrades.
- **Normalize never fails a retain on dates** (Hindsight never does; post-merge follow-up).
  - Every fallback error is logged, the fact keeps no dates, and Normalize returns nil. That covers an
    unbound slot, `ErrUnavailable` wrapping a jsonschema error (persistently invalid output after the
    gateway's retries), a plain `ErrUnavailable` (non-retryable 4xx), a gateway timeout, and a decode
    error.
  - Only the caller's own cancellation or deadline (`ctx.Err() != nil` or `context.Canceled`) is
    returned.
  - The earlier transient/permanent split let a broken TIME_PARSE model fail the retain job after its
    attempt cap.

### Forms added in review round 1
- **Day ranges.** "March 3-5, 2024" or "March 3 - 5 2024" is [Mar 3, Mar 6). The year is required, and a
  hyphen or en dash both work. A yearless "March 3-5" reads as March 3.
- **Range ends.** "now", "right now", "present" and "the present" as a range end mean the instant `ref`;
  "today" means the whole day. "since X until Y" is [X.Start, Y.End).
- **Two-digit years** fall in ref's century, minus 100 when that would be more than ref+20 years ahead.
  At ref 2026, 00-46 is 20yy and 47-99 is 19yy. This applies to slash dates and to "summer of '98" /
  "March '24".
- **ISO timestamps** ("2024-03-05T14:30:00Z", "2024-03-05 14:30") keep the date part only, as a day in
  ref's location.
- **Tighter cues.**
  - Bare "weekend(s)" is not a cue. It needs a determiner, or over/on/during/at before it.
  - "overnight" is never a cue.
  - before/after/until are not cues before a bare pronoun or possessive ("before I deploy",
    "after my workout"). They are cues before then/that, an event noun ("before my birthday"), or a
    pronoun plus a past-tense verb ("after we moved").

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
