package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
)

// linkRepo is store.LinkRepo over one transaction.
type linkRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.LinkRepo = (*linkRepo)(nil)

// Insert stores links; an existing (from, to, kind) edge is kept with the new weight. Self-links are
// skipped (the schema forbids them).
func (r *linkRepo) Insert(ctx context.Context, ls []domain.Link) error {
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
		err = r.q.LinkInsert(ctx, db.LinkInsertParams{
			TenantID: l.Bank.Tenant, BankID: l.Bank.Bank, FromUnitID: from, ToUnitID: to, Kind: string(l.Kind),
			Weight: l.Weight, CreatedAt: orNow(l.CreatedAt),
		})
		if err != nil {
			return mapErr("insert link", err)
		}
	}
	return nil
}

// DeleteForUnit removes the links of the given kinds (empty = all) touching unit in either direction and
// returns how many it removed.
func (r *linkRepo) DeleteForUnit(ctx context.Context, unit domain.UnitID, kinds []domain.LinkKind) (int, error) {
	uid, err := parseID(unit)
	if err != nil {
		return 0, err
	}
	ks := make([]string, len(kinds))
	for i, k := range kinds {
		ks[i] = string(k)
	}
	n, err := r.q.LinkDeleteForUnit(ctx, db.LinkDeleteForUnitParams{UnitID: uid, Kinds: ks})
	if err != nil {
		return 0, mapErr("delete unit links", err)
	}
	return int(n), nil
}

// Count returns how many links of kind touch unit in either direction.
func (r *linkRepo) Count(ctx context.Context, unit domain.UnitID, kind domain.LinkKind) (int, error) {
	uid, err := parseID(unit)
	if err != nil {
		return 0, err
	}
	n, err := r.q.LinkCount(ctx, db.LinkCountParams{UnitID: uid, Kind: string(kind)})
	if err != nil {
		return 0, mapErr("count unit links", err)
	}
	return int(n), nil
}

// Dirty returns up to limit live units queued in the bank's graph_dirty, oldest first. Queue rows of units
// removed since they were queued are skipped.
func (r *linkRepo) Dirty(ctx context.Context, b domain.BankRef, limit int) ([]domain.UnitID, error) {
	if limit <= 0 {
		return []domain.UnitID{}, nil
	}
	ids, err := r.q.LinkDirty(ctx, db.LinkDirtyParams{TenantID: b.Tenant, BankID: b.Bank, Lim: contentLimit(limit)})
	if err != nil {
		return nil, mapErr("dirty units", err)
	}
	return idsOf[domain.UnitID](ids), nil
}

// ClearDirty removes ids from the bank's graph_dirty.
func (r *linkRepo) ClearDirty(ctx context.Context, b domain.BankRef, ids []domain.UnitID) error {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return nil
	}
	err := r.q.LinkClearDirty(ctx, db.LinkClearDirtyParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	return mapErr("clear dirty units", err)
}
