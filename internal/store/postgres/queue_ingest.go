package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
)

// ActiveBySerialKey returns b's active jobs of kind with serial key key (pending or processing, no cancel
// requested), newest first.
func (r *queue) ActiveBySerialKey(
	ctx context.Context, b domain.BankRef, kind domain.OperationKind, key string,
) ([]store.Job, error) {
	rows, err := r.q.QueueActiveBySerialKey(ctx, db.QueueActiveBySerialKeyParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Kind: string(kind), SerialKey: key,
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("active %s jobs on %s", kind, key), err)
	}
	out := make([]store.Job, 0, len(rows))
	for _, row := range rows {
		j, err := jobOf(row)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// CountPending counts b's pending jobs of kind, stopping at limit.
func (r *queue) CountPending(ctx context.Context, b domain.BankRef, kind domain.OperationKind, limit int) (int, error) {
	n, err := r.q.QueueCountPendingInBank(ctx, db.QueueCountPendingInBankParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Kind: string(kind), Lim: toInt32Sat(max(limit, 0)),
	})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("count pending %s jobs of bank %s", kind, b), err)
	}
	return int(n), nil
}

// CountPendingInOrg counts the pending jobs of kind across o's banks, stopping at limit.
func (r *queue) CountPendingInOrg(
	ctx context.Context, o domain.OrgRef, kind domain.OperationKind, limit int,
) (int, error) {
	n, err := r.q.QueueCountPendingInOrg(ctx, db.QueueCountPendingInOrgParams{
		TenantID: o.Tenant, OrgID: o.Org, Kind: string(kind), Lim: toInt32Sat(max(limit, 0)),
	})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("count pending %s jobs of org %s", kind, o), err)
	}
	return int(n), nil
}
