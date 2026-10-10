# 122 — Temporal arm: overlap, proximity score and bucketing

## Context

`TemporalQuery` asks for units "whose occurrence (or mention) overlaps Window, spread over buckets", with a
"proximity" score. The port leaves the exact span, score and bucketing open.

## Decision

- A unit's span is `[COALESCE(occurred_start, occurred_end, mentioned_at), COALESCE(occurred_end,
  occurred_start, mentioned_at)]`: a unit with no occurrence falls back to the instant it was mentioned.
  Timeless units and units with no time never match.
- A hit overlaps the half-open window `[Start, End)`: span start < End and span end >= Start; a zero bound is
  unbounded.
- Score = `1 / (1 + |mid - anchor| in days)` in (0, 1], where mid is the midpoint of the span clamped to the
  window and anchor is the window's midpoint (or its only bound when the other is open).
- With `Buckets > 1` and both bounds set, the window is cut into `Buckets` equal sub-windows by mid and hits are
  taken round robin, best first in each, so a dense stretch can't crowd out the rest; the selected hits are
  returned best first.

## Alternatives

- Occurrence only (ignoring mentioned_at): loses facts whose extraction found no explicit date.
- Scoring by recency: that is the boost stage's job, not the arm's.

## Spec lines involved

SPEC "Recall pipeline" (temporal arm: window, bucketed); `internal/port/queries.go` (TemporalQuery).
