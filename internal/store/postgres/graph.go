package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store/postgres/db"
)

// graphStore is the memory graph over memory_links: graph.Tx over one transaction (tx.Graph), or graph.Reader over
// the pool (DB.Graph, each call its own statement).
type graphStore struct {
	conn db.DBTX
	q    *db.Queries
}

var _ graph.Tx = (*graphStore)(nil)

// newGraph returns the graph over conn.
func newGraph(conn db.DBTX) *graphStore { return &graphStore{conn: conn, q: db.New(conn)} }

// Graph returns the long-lived read-only graph over the pool: the recall graph arm's Reader. Writes go through
// store.Tx.Graph.
func (s *DB) Graph() graph.Reader { return newGraph(s.pool) }

// Graph returns the graph bound to this transaction.
func (t *tx) Graph() graph.Tx { return newGraph(t.pgTx) }

// Expand is the graph arm: a recursive CTE over the bank's forward links from the seeds, at most MaxHops (at least
// 1) hops, scored by the best path weight. Types, Tags and Seq are applied in SQL before the limit.
func (g *graphStore) Expand(ctx context.Context, q graph.Query) ([]graph.Hit, error) {
	seeds := parseIDs(q.Seeds)
	if q.Limit <= 0 || len(seeds) == 0 {
		return []graph.Hit{}, nil
	}
	sql, args := graphSQL(q, seeds)
	hs, err := queryHits(ctx, g.conn, "expand graph", sql, args)
	if err != nil {
		return nil, err
	}
	out := make([]graph.Hit, len(hs))
	for i, h := range hs {
		out[i] = graph.Hit{ID: h.ID, Score: h.Score}
	}
	return out, nil
}

// AddLinks stores links; an existing (from, to, kind) edge is kept with the new weight. Self-links are skipped
// (the schema forbids them).
func (g *graphStore) AddLinks(ctx context.Context, ls []graph.Link) error {
	for _, l := range ls {
		from, err := parseID(l.From)
		if err != nil {
			return err
		}
		to, err := parseID(l.To)
		if err != nil {
			return err
		}
		if from == to {
			continue
		}
		err = g.q.LinkInsert(ctx, db.LinkInsertParams{
			TenantID: l.Bank.Tenant, BankID: l.Bank.Bank, FromUnitID: from, ToUnitID: to, Kind: string(l.Kind),
			Weight: l.Weight, CreatedAt: orNow(l.CreatedAt),
		})
		if err != nil {
			return mapErr("insert link", err)
		}
	}
	return nil
}

// DeleteUnitLinks removes the links of the given kinds (empty = all) touching unit in either direction and
// returns how many it removed.
func (g *graphStore) DeleteUnitLinks(ctx context.Context, unit domain.UnitID, kinds []graph.Kind) (int, error) {
	uid, err := parseID(unit)
	if err != nil {
		return 0, err
	}
	ks := make([]string, len(kinds))
	for i, k := range kinds {
		ks[i] = string(k)
	}
	n, err := g.q.LinkDeleteForUnit(ctx, db.LinkDeleteForUnitParams{UnitID: uid, Kinds: ks})
	if err != nil {
		return 0, mapErr("delete unit links", err)
	}
	return int(n), nil
}

// Degree returns how many links of kind touch unit in either direction.
func (g *graphStore) Degree(ctx context.Context, unit domain.UnitID, kind graph.Kind) (int, error) {
	uid, err := parseID(unit)
	if err != nil {
		return 0, err
	}
	n, err := g.q.LinkCount(ctx, db.LinkCountParams{UnitID: uid, Kind: string(kind)})
	if err != nil {
		return 0, mapErr("count unit links", err)
	}
	return int(n), nil
}

// DirtyLinks returns up to limit live units queued in the bank's graph_dirty, oldest first. Queue rows of units
// removed since they were queued are skipped.
func (r *unitRepo) DirtyLinks(ctx context.Context, b domain.BankRef, limit int) ([]domain.UnitID, error) {
	if limit <= 0 {
		return []domain.UnitID{}, nil
	}
	ids, err := r.q.LinkDirty(ctx, db.LinkDirtyParams{TenantID: b.Tenant, BankID: b.Bank, Lim: contentLimit(limit)})
	if err != nil {
		return nil, mapErr("dirty units", err)
	}
	return idsOf[domain.UnitID](ids), nil
}

// ClearDirtyLinks removes ids from the bank's graph_dirty.
func (r *unitRepo) ClearDirtyLinks(ctx context.Context, b domain.BankRef, ids []domain.UnitID) error {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return nil
	}
	err := r.q.LinkClearDirty(ctx, db.LinkClearDirtyParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	return mapErr("clear dirty units", err)
}
