package postgres

import (
	"memd/internal/store"
	"strings"
	"time"
)

// Temporal arm SQL (docs/design/storage.md#search-query-shapes). A unit's time span is [us, ue]:
//
//	us = COALESCE(occurred_start, occurred_end, mentioned_at)
//	ue = COALESCE(occurred_end, occurred_start, mentioned_at)
//
// so a unit with no occurrence falls back to the instant it was mentioned. Timeless units and units with no
// time at all never match. A unit is a hit when its span overlaps the half-open window [Start, End): us < End
// and ue >= Start, a zero bound being unbounded.
const (
	temporalStart = "COALESCE(occurred_start, occurred_end, mentioned_at)"
	temporalEnd   = "COALESCE(occurred_end, occurred_start, mentioned_at)"
	secondsPerDay = "86400.0"
)

// epoch returns t as Unix seconds for the SQL arithmetic, or nil (SQL NULL) when t is zero (unbounded).
func epoch(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}
	s := float64(t.UnixMicro()) / 1e6
	return &s
}

// windowAnchor is the point proximity is measured from: the window's midpoint, or its only bound when the
// other is open; nil when both are open.
func windowAnchor(w store.TemporalQuery) *float64 {
	s, e := epoch(w.Window.Start), epoch(w.Window.End)
	switch {
	case s != nil && e != nil:
		m := (*s + *e) / 2
		return &m
	case s != nil:
		return s
	default:
		return e
	}
}

// temporalFilterOn reports whether the relevance filter applies: an embedding and a positive floor.
func temporalFilterOn(q store.TemporalQuery) bool {
	return len(q.Embedding) > 0 && q.MinSimilarity > 0
}

// temporalRelevance returns the relevance filter's WHERE fragment (with a leading " AND "), or "" when it is
// off: units without an embedding are excluded and the rest need cosine similarity >= MinSimilarity. It sits
// in the span CTE, so it applies before bucketing and the limit; the hit score stays the temporal score.
func temporalRelevance(q store.TemporalQuery, a *sqlArgs) string {
	if !temporalFilterOn(q) {
		return ""
	}
	return " AND embedding IS NOT NULL AND 1 - (embedding <=> " + a.add(vec(q.Embedding), "::vector") +
		") >= " + a.add(q.MinSimilarity, "::float8")
}

// temporalSQL builds the temporal query. The CTE "hit" holds every overlapping unit with mid, the midpoint
// of its span clamped to the window (GREATEST / LEAST ignore the NULL of an open bound), and its score
// 1 / (1 + |mid - anchor| in days), in (0, 1], higher = closer to the window's center. With Buckets > 1 and
// both bounds set, the window is cut into Buckets equal sub-windows by mid and the hits are taken round
// robin, best first within each sub-window, so a dense stretch can't crowd out the rest; the selected hits
// are then returned best first.
func temporalSQL(q store.TemporalQuery) (string, []any) {
	var a sqlArgs
	where := unitFilter{bank: q.Bank, types: q.Types, tags: q.Tags, seq: q.Seq}.render("", &a)
	start, end := epoch(q.Window.Start), epoch(q.Window.End)
	var b strings.Builder
	b.WriteString("WITH span AS (SELECT id, extract(epoch FROM " + temporalStart + ")::float8 AS us, extract(epoch FROM ")
	b.WriteString(temporalEnd + ")::float8 AS ue FROM memory_units WHERE NOT timeless AND " + temporalStart)
	b.WriteString(" IS NOT NULL AND " + where)
	if !q.Window.End.IsZero() {
		b.WriteString(" AND " + temporalStart + " < " + a.add(q.Window.End, "::timestamptz"))
	}
	if !q.Window.Start.IsZero() {
		b.WriteString(" AND " + temporalEnd + " >= " + a.add(q.Window.Start, "::timestamptz"))
	}
	b.WriteString(temporalRelevance(q, &a))
	b.WriteString("), mid AS (SELECT id, (GREATEST(us, " + a.add(start, "::float8") + ") + LEAST(ue, ")
	b.WriteString(a.add(end, "::float8") + ")) / 2 AS mid FROM span), hit AS (SELECT id, mid, ")
	if anchor := windowAnchor(q); anchor != nil {
		b.WriteString("1 / (1 + abs(mid - " + a.add(*anchor, "::float8") + ") / " + secondsPerDay + ")")
	} else {
		b.WriteString("1::float8")
	}
	b.WriteString(" AS score FROM mid) ")
	limit := a.add(q.Limit, "::int")
	if q.Buckets <= 1 || start == nil || end == nil {
		b.WriteString("SELECT id, score FROM hit ORDER BY score DESC, id LIMIT " + limit)
		return b.String(), a.vals
	}
	b.WriteString("SELECT id, score FROM (SELECT id, score FROM (SELECT id, score, row_number() OVER (PARTITION BY ")
	b.WriteString("width_bucket(mid, " + a.add(*start, "::float8") + ", " + a.add(*end, "::float8") + ", ")
	b.WriteString(a.add(q.Buckets, "::int") + ") ORDER BY score DESC, id) AS rn FROM hit) r")
	b.WriteString(" ORDER BY rn, score DESC, id LIMIT " + limit + ") picked ORDER BY score DESC, id")
	return b.String(), a.vals
}
