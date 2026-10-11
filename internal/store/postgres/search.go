package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The four recall arms. Their SQL is built at run time (see predicate.go) and every value is a bind
// parameter. Each arm reads only memory_units, which holds live units (archived ones move to
// memory_units_archive), and orders ties by id so results are deterministic.

// HNSW settings of the vector search (SPEC "Recall pipeline"; decision 120).
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

// SearchText serves the text arm: pg_search BM25 over unit text and context (decision 121). The raw query
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
// proximity and spread over q.Buckets sub-windows (decision 122).
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
