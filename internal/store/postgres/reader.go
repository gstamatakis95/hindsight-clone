package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reader is store.Reader over either the pool (Store.Read: each call its own statement) or one transaction
// (embedded in tx). The search arms live in search.go.
type reader struct {
	// conn runs the statements: the pool or the caller's pgx.Tx.
	conn db.DBTX
	// pool is set only for the pool-backed reader; calls that need transaction-local settings open a short
	// read-only transaction on it.
	pool *pgxpool.Pool
}

// queries returns sqlc queries bound to the reader's connection.
func (r *reader) queries() *db.Queries { return db.New(r.conn) }

// inReadTx runs fn in a transaction: the caller's own when the reader belongs to a Tx, else a short
// read-only one on the pool. Use it for statements that need SET LOCAL (set_config(..., true)).
func (r *reader) inReadTx(ctx context.Context, fn func(pgx.Tx) error) error {
	if r.pool == nil {
		pgTx, ok := r.conn.(pgx.Tx)
		if !ok {
			return fmt.Errorf("%w: reader has neither a pool nor a transaction", domain.ErrUnavailable)
		}
		return fn(pgTx)
	}
	pgTx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapErr("begin read", err)
	}
	defer func() { _ = pgTx.Rollback(context.WithoutCancel(ctx)) }() // read-only: nothing to keep
	return fn(pgTx)
}

// GetUnits returns the live units with the given ids in the order of ids; unknown, archived and other-bank
// ids are skipped.
func (r *reader) GetUnits(ctx context.Context, b domain.BankRef, ids []domain.UnitID) ([]domain.Unit, error) {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return []domain.Unit{}, nil
	}
	rows, err := r.queries().GetUnits(ctx, db.GetUnitsParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return nil, mapErr("get units", err)
	}
	byID := make(map[domain.UnitID]domain.Unit, len(rows))
	for _, row := range rows {
		u, err := unitOf(row.MemoryUnit, row.EntityNames, row.SourceIds)
		if err != nil {
			return nil, err
		}
		byID[u.ID] = u
	}
	out := make([]domain.Unit, 0, len(rows))
	for _, id := range uuids {
		if u, ok := byID[idOf[domain.UnitID](id)]; ok {
			out = append(out, u)
			delete(byID, u.ID) // a repeated id is returned once
		}
	}
	return out, nil
}

// BankSeq returns the bank's latest committed change_seq; unknown = domain.ErrNotFound.
func (r *reader) BankSeq(ctx context.Context, b domain.BankRef) (int64, error) {
	seq, err := r.queries().BankChangeSeq(ctx, db.BankChangeSeqParams{TenantID: b.Tenant, OrgID: b.Org, ID: b.Bank})
	if err != nil {
		return 0, bankErr(b, err)
	}
	return seq, nil
}
