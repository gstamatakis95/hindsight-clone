package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The four recall arms. Their SQL is built at run time (see predicate.go) and every value is a bind
// parameter. Each arm reads only memory_units, which holds live units (archived ones move to
// memory_units_archive), and orders ties by id so results are deterministic.

// HNSW settings of the vector search (docs/design/storage.md#search-query-shapes).
const (
	hnswEfSearch      = "200"
	hnswIterativeScan = "strict_order"
	hnswMaxScanTuples = "100000"
)

// SearchVector serves the vector arm: HNSW cosine search with iterative scan. Score = cosine similarity
// (1 - cosine distance); units without an embedding never match.
func (r *reader) SearchVector(ctx context.Context, q store.VectorQuery) ([]store.Hit, error) {
	if q.Limit <= 0 || len(q.Embedding) == 0 {
		return []store.Hit{}, nil
	}
	if len(q.Embedding) != domain.EmbeddingDim {
		return nil, fmt.Errorf("%w: query embedding has %d dims, want %d",
			domain.ErrInvalidArgument, len(q.Embedding), domain.EmbeddingDim)
	}
	sql, args := vectorSQL(q)
	var hits []store.Hit
	err := r.inReadTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT set_config('hnsw.ef_search', $1, true),
			set_config('hnsw.iterative_scan', $2, true), set_config('hnsw.max_scan_tuples', $3, true)`,
			hnswEfSearch, hnswIterativeScan, hnswMaxScanTuples)
		if err != nil {
			return mapErr("set hnsw options", err)
		}
		hits, err = queryHits(ctx, tx, "search vector", sql, args)
		return err
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// vectorSQL builds the semantic query. The inner query keeps the index-friendly shape (filters in WHERE,
// ORDER BY the distance operator, LIMIT) so HNSW with iterative scan can serve it; the similarity floor is
// applied outside, which keeps the same top hits because the inner order is by distance.
func vectorSQL(q store.VectorQuery) (string, []any) {
	var a sqlArgs
	v := a.add(vec(q.Embedding), "::vector")
	where := unitFilter{bank: q.Bank, types: q.Types, tags: q.Tags, seq: q.Seq}.render("", &a)
	var b strings.Builder
	b.WriteString("SELECT id, 1 - dist AS score FROM (SELECT id, embedding <=> " + v + " AS dist")
	b.WriteString(" FROM memory_units WHERE embedding IS NOT NULL AND " + where)
	b.WriteString(" ORDER BY embedding <=> " + v + " LIMIT " + a.add(q.Limit, "::int") + ") s")
	if q.MinSimilarity != 0 {
		b.WriteString(" WHERE 1 - dist >= " + a.add(q.MinSimilarity, "::float8"))
	}
	b.WriteString(" ORDER BY dist, id")
	return b.String(), a.vals
}

// SearchText serves the text arm: pg_search BM25 over unit text and context. The raw query
// text goes through the ||| (match disjunction) operator, which tokenizes it with the field's tokenizer
// instead of parsing query syntax, so quotes and colons in user text are plain words. Score =
// paradedb.score(id); an empty or blank query has no hits.
func (r *reader) SearchText(ctx context.Context, q store.TextQuery) ([]store.Hit, error) {
	if q.Limit <= 0 || strings.TrimSpace(q.Text) == "" {
		return []store.Hit{}, nil
	}
	var a sqlArgs
	text := a.add(q.Text, "::text")
	where := unitFilter{bank: q.Bank, types: q.Types, tags: q.Tags, seq: q.Seq}.render("", &a)
	var b strings.Builder
	b.WriteString("SELECT id, score FROM (SELECT id, paradedb.score(id)::float8 AS score FROM memory_units")
	b.WriteString(" WHERE (text ||| " + text + " OR context ||| " + text + ") AND " + where)
	b.WriteString(" ORDER BY score DESC LIMIT " + a.add(q.Limit, "::int") + ") s")
	if q.MinScore != 0 {
		b.WriteString(" WHERE score >= " + a.add(q.MinScore, "::float8"))
	}
	b.WriteString(" ORDER BY score DESC, id")
	return queryHits(ctx, r.conn, "search text", b.String(), a.vals)
}

// SearchTemporal serves the temporal arm: units whose occurrence overlaps the half-open window, scored by
// proximity and spread over q.Buckets sub-windows.
func (r *reader) SearchTemporal(ctx context.Context, q store.TemporalQuery) ([]store.Hit, error) {
	w := q.Window
	if q.Limit <= 0 || (!w.Start.IsZero() && !w.End.IsZero() && !w.End.After(w.Start)) {
		return []store.Hit{}, nil // an empty or inverted window holds nothing
	}
	sql, args := temporalSQL(q)
	return queryHits(ctx, r.conn, "search temporal", sql, args)
}

// queryHits runs a query returning (id uuid, score float8) rows and converts them to hits in row order.
func queryHits(ctx context.Context, conn db.DBTX, what, sql string, args []any) ([]store.Hit, error) {
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(what, err)
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.Hit, error) {
		var id uuid.UUID
		var score float64
		err := row.Scan(&id, &score)
		return store.Hit{ID: idOf[domain.UnitID](id), Score: score}, mapErr("scan hit", err)
	})
	if err != nil {
		return nil, mapErr(what, err)
	}
	return hits, nil
}

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
