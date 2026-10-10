package postgres_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Acceptance fixture: acceptUnits units with random embeddings, one in every acceptSmallEvery (0.1%) in
// bank t1/small and the rest spread over a big bank of the same tenant and two banks of another tenant
// (one also named "small").
const (
	acceptUnits      = 50000
	acceptSmallEvery = 1000
)

// acceptSmall is the small bank searched.
var acceptSmall = port.BankRef{Tenant: "t1", Bank: "small"}

// loadAcceptFixture inserts the banks and units in SQL (random vectors generated server-side) and analyzes
// the table. It returns the ids of the small bank's units.
func loadAcceptFixture(t *testing.T, pool *pgxpool.Pool) map[port.UnitID]bool {
	t.Helper()
	ctx := t.Context()
	start := time.Now()
	var indexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND indexname = 'memory_units_embedding_hnsw'`).Scan(&indexDef); err != nil {
		t.Fatalf("hnsw index definition: %v", err)
	}
	// Inserting row by row into a live HNSW index takes minutes; one transaction drops the index, loads the
	// rows and rebuilds it from its own catalog definition (a rollback restores it), which takes seconds.
	stmts := []string{
		`SET LOCAL maintenance_work_mem = '512MB'`,
		`SET LOCAL max_parallel_maintenance_workers = 4`,
		`INSERT INTO banks (tenant_id, id) VALUES ('t1', 'small'), ('t1', 'big'), ('t2', 'small'), ('t2', 'big')`,
		`DROP INDEX memory_units_embedding_hnsw`,
		// The subquery references g (in WHERE), so it runs once per row (an uncorrelated one would give every row
		// the same vector).
		`INSERT INTO memory_units (tenant_id, bank_id, fact_type, text, seq, embedding)
		 SELECT CASE WHEN g % 1000 = 0 OR g % 2 = 0 THEN 't1' ELSE 't2' END,
		        CASE WHEN g % 1000 = 0 THEN 'small' WHEN g % 2 = 0 OR g % 4 = 1 THEN 'big' ELSE 'small' END,
		        'world', 'unit ' || g, 1,
		        (SELECT array_agg(random() - 0.5) FROM generate_series(1, 768) WHERE g > 0)::vector
		 FROM generate_series(1, 50000) g`,
		indexDef,
	}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s); err != nil {
				return fmt.Errorf("%w\n%s", err, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE memory_units`); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	t.Logf("loaded %d units in %s", acceptUnits, time.Since(start).Round(time.Millisecond))
	rows, err := pool.Query(ctx, `SELECT id::text FROM memory_units WHERE tenant_id = 't1' AND bank_id = 'small'`)
	if err != nil {
		t.Fatalf("small ids: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("small ids: %v", err)
	}
	if len(ids) != acceptUnits/acceptSmallEvery {
		t.Fatalf("small bank has %d units, want %d", len(ids), acceptUnits/acceptSmallEvery)
	}
	small := make(map[port.UnitID]bool, len(ids))
	for _, id := range ids {
		small[port.UnitID(id)] = true
	}
	return small
}

// randomVec returns a random query embedding.
func randomVec() []float32 {
	v := make([]float32, domain.EmbeddingDim)
	for i := range v {
		v[i] = rand.Float32() - 0.5 //nolint:gosec // test data, not security
	}
	return v
}

// explainVector EXPLAINs the semantic arm's query shape (see vectorSQL) under the arm's HNSW settings, with
// iterative scan set to iterative, and returns the plan text.
func explainVector(ctx context.Context, t *testing.T, pool *pgxpool.Pool, q []float32, iterative string) string {
	t.Helper()
	var plan []string
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('hnsw.ef_search', '200', true),
			set_config('hnsw.iterative_scan', $1, true), set_config('hnsw.max_scan_tuples', '100000', true)`,
			iterative); err != nil {
			return fmt.Errorf("explain query: %w", err)
		}
		rows, err := tx.Query(ctx, `EXPLAIN SELECT id, 1 - dist AS score FROM (SELECT id, embedding <=> $1::vector
			AS dist FROM memory_units WHERE embedding IS NOT NULL AND tenant_id = $2::text AND bank_id = $3::text
			ORDER BY embedding <=> $1::vector LIMIT $4::int) s ORDER BY dist, id`,
			pgvector.NewVector(q), acceptSmall.Tenant, acceptSmall.Bank, 10)
		if err != nil {
			return fmt.Errorf("explain query: %w", err)
		}
		if plan, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return fmt.Errorf("explain rows: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	return strings.Join(plan, "\n")
}

// forcedIndexPool returns a pool on the same database whose sessions disable sequential, bitmap and sort
// plans, so the planner must answer ORDER BY embedding <=> q LIMIT n from the HNSW index.
func forcedIndexPool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	for _, k := range []string{"enable_seqscan", "enable_bitmapscan", "enable_sort"} {
		cfg.ConnConfig.RuntimeParams[k] = "off"
	}
	forced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("forced pool: %v", err)
	}
	t.Cleanup(forced.Close)
	return forced
}

// wantSmallHits fails unless SearchVector over s returns 10 hits, all of the small bank.
func wantSmallHits(t *testing.T, what string, s *postgres.Store, q []float32, small map[port.UnitID]bool) {
	t.Helper()
	start := time.Now()
	hs, err := s.Read().SearchVector(t.Context(), port.VectorQuery{Bank: acceptSmall, Embedding: q, Limit: 10})
	if err != nil {
		t.Fatalf("%s: SearchVector: %v", what, err)
	}
	t.Logf("%s: %d hits in %s", what, len(hs), time.Since(start).Round(time.Microsecond))
	if len(hs) != 10 {
		t.Errorf("%s: %d hits, want 10", what, len(hs))
	}
	for _, h := range hs {
		if !small[h.ID] {
			t.Errorf("%s: hit %s is not a unit of the small bank", what, h.ID)
		}
	}
}

// TestAcceptSmallBankVector: on a shared HNSW index where the searched bank holds 0.1% of the rows,
// SearchVector with Limit 10 still returns 10 hits, all of that bank, also when the plan goes through HNSW
// (iterative scan keeps scanning past the filtered-out neighbours).
func TestAcceptSmallBankVector(t *testing.T) {
	if testing.Short() {
		t.Skip("loads 50,000 units")
	}
	pool := pgtest.Fresh(t)
	small := loadAcceptFixture(t, pool)
	q := randomVec()

	plan := explainVector(t.Context(), t, pool, q, "strict_order")
	t.Logf("default plan uses HNSW: %v\n%s", strings.Contains(plan, "memory_units_embedding_hnsw"), plan)
	wantSmallHits(t, "default plan", postgres.New(pool), q, small)

	forced := forcedIndexPool(t, pool)
	plan = explainVector(t.Context(), t, forced, q, "strict_order")
	if !strings.Contains(plan, "memory_units_embedding_hnsw") {
		t.Fatalf("forced plan does not use the HNSW index:\n%s", plan)
	}
	wantSmallHits(t, "forced HNSW plan", postgres.New(forced), q, small)

	// Negative control: without iterative scan the HNSW path stops after ef_search candidates, almost none
	// of them in the small bank.
	n := countWithoutIterativeScan(t, forced, q)
	t.Logf("forced HNSW plan without iterative scan: %d hits", n)
}

// countWithoutIterativeScan runs the semantic query shape on pool with hnsw.iterative_scan off and returns
// the number of hits.
func countWithoutIterativeScan(t *testing.T, pool *pgxpool.Pool, q []float32) int {
	t.Helper()
	ctx := t.Context()
	var n int
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('hnsw.ef_search', '200', true),
			set_config('hnsw.iterative_scan', 'off', true)`); err != nil {
			return fmt.Errorf("explain query: %w", err)
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM memory_units WHERE embedding IS NOT NULL
			AND tenant_id = $2::text AND bank_id = $3::text ORDER BY embedding <=> $1::vector LIMIT 10) s`,
			pgvector.NewVector(q), acceptSmall.Tenant, acceptSmall.Bank).Scan(&n)
	})
	if err != nil {
		t.Fatalf("query without iterative scan: %v", err)
	}
	return n
}
