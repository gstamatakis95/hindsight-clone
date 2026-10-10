package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// linkRepo is port.LinkRepo over one transaction.
type linkRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.LinkRepo = (*linkRepo)(nil)

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

// DeleteForUnit is not implemented yet.
func (r *linkRepo) DeleteForUnit(ctx context.Context, unit port.UnitID, kinds []domain.LinkKind) (int, error) {
	return 0, errNotImplemented
}

// Count is not implemented yet.
func (r *linkRepo) Count(ctx context.Context, unit port.UnitID, kind domain.LinkKind) (int, error) {
	return 0, errNotImplemented
}

// Dirty is not implemented yet.
func (r *linkRepo) Dirty(ctx context.Context, b port.BankRef, limit int) ([]port.UnitID, error) {
	return nil, errNotImplemented
}

// ClearDirty is not implemented yet.
func (r *linkRepo) ClearDirty(ctx context.Context, b port.BankRef, ids []port.UnitID) error {
	return errNotImplemented
}
