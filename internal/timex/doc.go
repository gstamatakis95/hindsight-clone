// Package timex resolves time for retain and recall. At retain time it normalizes an extracted fact's
// occurrence from its raw time expression ("last spring") against the item's reference time; at query time
// it turns a temporal cue in a recall query into a domain.TimeWindow for recall's temporal arm.
//
// # Where it sits
//
// timex is a library: it imports domain, ai, clock and prompts and must never import a service, an adapter, gen,
// pgx, net/http, the S3 SDK or Datadog (depguard). It works on domain.ExtractedFact rather than a retain type so
// that retain can import timex without a cycle. cmd/memd/wire.go builds one [Normalizer] with
// [New](gateway, router, clock) and hands it to retain and recall (its query parser).
//
// # Usage
//
// Both methods take the caller's effective, already merged domain.BankConfig, so a per-request TIME_PARSE
// override reaches the fallback call:
//
//	// retain: fills f.OccurredStart / f.OccurredEnd in place
//	err := times.Normalize(ctx, cfg, &fact, ref) // ref: the item's timestamp
//
//	// recall: nil window = no time constraint
//	w, err := times.QueryWindow(ctx, cfg, "what did I do last spring?", time.Time{})
//
// A zero reference time means "now" from the injected clock.Clock; timex never calls time.Now.
//
// # Flow
//
// Normalize(ctx, cfg, f, ref):
//
//  1. A nil fact, a fact that already has OccurredStart, or an empty TimeExpression is left unchanged.
//  2. The deterministic rules parse the expression against ref.
//  3. If no rule resolves it, the TIME_PARSE fallback runs (below).
//  4. The window's bounds become OccurredStart and OccurredEnd; OccurredEnd is the exclusive end, and an
//     unbounded side stays nil.
//
// QueryWindow(ctx, cfg, query, ref):
//
//  1. The lexical cue gate runs first. No temporal cue means (nil, nil): no window, no router lookup, no
//     model call.
//  2. The rules parse the query; a resolved window is returned.
//  3. Otherwise the TIME_PARSE fallback runs.
//
// The fallback resolves the TIME_PARSE binding through ai.Router only at this point (so an unbound
// slot never fails a call the rules can answer), renders the time_parse_system / time_parse_user prompts
// with the reference instant, weekday, time zone, kind ("query" or "expression") and text, and makes one
// ai.LLM.Structured call returning {start, end} as RFC 3339 or YYYY-MM-DD strings (null = unbounded).
// Both bounds null, an unparseable bound, start >= end, or a bound outside years 1..9999 yields no window
// (not an error).
//
// # Errors
//
// Neither method fails its caller over time analysis. Any fallback error (unbound slot, gateway
// unavailable, persistently invalid output, gateway timeout) is logged with slog and degrades: Normalize
// returns nil and the fact keeps no dates; QueryWindow returns (nil, nil) and recall runs without a time
// constraint. Only the caller's own cancellation or deadline (ctx.Err() != nil, or context.Canceled) is
// returned. A gateway per-call timeout under a live ctx still degrades.
//
// # Rule semantics
//
// The rules are hand-written with regexp and the standard library, English only:
//
//   - All arithmetic uses ref in ref.Location() (wall-clock, DST-safe); weeks start on Monday; every window
//     is half-open [Start, End).
//   - Priority: "the day before yesterday" / "the day after tomorrow", then ranges, then point expressions
//     (absolute dates, year-qualified months, seasons, quarters and decades, relative expressions, named
//     periods, bare years). Within a rule the leftmost match wins.
//   - Relative: today/yesterday/tomorrow; "N units ago" is that whole calendar unit; "last/next N units"
//     runs from midnight N units back (or forward) through the end of today; "this/last/next
//     week|month|year|quarter|decade|weekend" is the calendar period; "earlier/later this year" splits the
//     period at ref; "recently"/"lately" is the last 30 days through the end of today. In "N units ago" a
//     vague count (couple = 2, few/several = 3) widens to the units N-1 through N+1 back.
//   - Named periods without a year resolve to the most recent occurrence starting no later than ref; "last"
//     is the most recent one already ended, "this" the one in the current week/year/season, "next" the first
//     after ref. Seasons are northern-hemisphere meteorological (winter Y = December Y to February Y+1).
//     Month and year shifts clamp to the month's last day.
//   - Absolute dates: ISO (a time suffix is dropped; only the day is kept), "March 5 2024", "5 March 2024",
//     day ranges "March 3-5 2024"; slash dates are US m/d/y, swapped to d/m when the first number exceeds 12.
//     A two-digit year falls in ref's century, or the century before when that would be more than 20 years
//     after ref. A bare four-digit year counts only within ref-120..ref+20 years and not next to an
//     identifier ("port 2019", "$2000").
//   - Ranges: between/from X to Y is [X.Start, Y.End) (an endpoint may borrow the other's year); since X is
//     [X.Start, ref); until X is [unbounded, X.End); before X is [unbounded, X.Start); after X is [X.End,
//     unbounded). "now"/"present" as an endpoint is the instant ref.
//   - A rule window with a bound outside years 1..9999 is consumed as no window, so no fallback call is made.
//
// The cue gate counts every form the rules resolve plus vaguer phrasing left to the fallback ("the other
// day", "around the holidays", "when I was in college"). Ambiguous words count only in a temporal frame:
// "in March" but not "march on" or "may I ask"; "last fall" but not "fall down"; a four-digit number only
// when it reads as a year. "now", "date", "overnight" and a bare "weekend" are not cues.
//
// # Concurrency and tests
//
// The normalizer holds only its injected dependencies and compiled regexps, so it is safe for concurrent
// use. corpus_test.go and timex_test.go run a query corpus at a fixed reference (including non-UTC zones),
// check that no cue means no LLM call, that the fallback runs only with a cue and no rule, and that every
// fallback failure degrades; rules_test.go and cue_test.go pin individual rules and cues. The LLM is the
// generated mockai mock, with the real ai.Router and a clock.Manual. SPEC "Model routing" (the TIME_PARSE slot)
// and "Recall pipeline" (the temporal arm).
package timex
