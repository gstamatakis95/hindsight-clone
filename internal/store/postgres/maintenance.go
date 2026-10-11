package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"
)

// The cross-bank reads of store.Maintenance; StrandedFacts and SettleRetainBatches live in stranded.go and settle.go.

var _ store.Maintenance = (*DB)(nil)

// UnconsolidatedBanks returns, ordered by (tenant, bank), every bank that is not tombstoned and holds an
// unconsolidated fact with an embedding created before before, with its oldest such fact's created_at.
func (s *DB) UnconsolidatedBanks(ctx context.Context, before time.Time) ([]store.UnconsolidatedBank, error) {
	rows, err := db.New(s.pool).UnconsolidatedBanks(ctx, before)
	if err != nil {
		return nil, mapErr("unconsolidated banks", err)
	}
	out := make([]store.UnconsolidatedBank, len(rows))
	for i, row := range rows {
		out[i] = store.UnconsolidatedBank{Bank: domain.BankRef{Tenant: row.TenantID, Bank: row.ID}, Oldest: row.Oldest}
	}
	return out, nil
}

// StalledBankDeletes returns, ordered by (tenant, bank), every tombstoned bank with no pending or processing
// delete_bank operation.
func (s *DB) StalledBankDeletes(ctx context.Context) ([]domain.BankRef, error) {
	rows, err := db.New(s.pool).StalledBankDeletes(ctx)
	if err != nil {
		return nil, mapErr("stalled bank deletes", err)
	}
	out := make([]domain.BankRef, len(rows))
	for i, row := range rows {
		out[i] = domain.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}

// BanksWithGraphWork returns, ordered by (tenant, bank), every bank that is not tombstoned and has
// graph_dirty or entity_maintenance rows queued.
func (s *DB) BanksWithGraphWork(ctx context.Context) ([]domain.BankRef, error) {
	rows, err := db.New(s.pool).BanksWithGraphWork(ctx)
	if err != nil {
		return nil, mapErr("banks with graph work", err)
	}
	out := make([]domain.BankRef, len(rows))
	for i, row := range rows {
		out[i] = domain.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}

// CronModels returns every model with a non-empty refresh_cron on a bank that is not tombstoned, with the
// created_at of its newest refresh operation (zero when none), ordered by (tenant, bank, model id).
func (s *DB) CronModels(ctx context.Context) ([]store.CronModel, error) {
	rows, err := db.New(s.pool).ReadsCronModels(ctx)
	if err != nil {
		return nil, mapErr("cron models", err)
	}
	out := make([]store.CronModel, len(rows))
	for i, row := range rows {
		m, err := modelOf(row.MentalModel)
		if err != nil {
			return nil, err
		}
		var last time.Time
		if row.Enqueued {
			last = row.LastEnqueuedAt
		}
		out[i] = store.CronModel{Model: m, LastRefreshEnqueuedAt: last}
	}
	return out, nil
}

// FailedRefreshes returns, per existing model on a bank that is not tombstoned, its latest completed or failed
// refresh operation when that one failed (cancelled operations are ignored), ordered by (tenant, bank, model
// id).
func (s *DB) FailedRefreshes(ctx context.Context) ([]store.FailedRefresh, error) {
	rows, err := db.New(s.pool).ReadsFailedRefreshes(ctx)
	if err != nil {
		return nil, mapErr("failed refreshes", err)
	}
	out := make([]store.FailedRefresh, len(rows))
	for i, row := range rows {
		f := store.FailedRefresh{
			Bank: domain.BankRef{Tenant: row.TenantID, Bank: row.BankID}, ModelID: domain.MentalModelID(row.ModelID),
			OperationID: idOf[domain.OperationID](row.OperationID), Error: row.Error,
		}
		if row.FinishedAt != nil {
			f.FailedAt = *row.FinishedAt
		}
		out[i] = f
	}
	return out, nil
}
