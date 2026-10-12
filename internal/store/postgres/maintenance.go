package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"
)

// The cross-bank reads of store.Maintenance.

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
		out[i] = store.UnconsolidatedBank{Bank: domain.NewBankRef(row.TenantID, row.OrgID, row.ID), Oldest: row.Oldest}
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
		out[i] = domain.NewBankRef(row.TenantID, row.OrgID, row.ID)
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
		out[i] = domain.NewBankRef(row.TenantID, row.OrgID, row.ID)
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
			Bank: domain.NewBankRef(row.TenantID, row.OrgID, row.BankID), ModelID: domain.MentalModelID(row.ModelID),
			OperationID: idOf[domain.OperationID](row.OperationID), Error: row.Error,
		}
		if row.FinishedAt != nil {
			f.FailedAt = *row.FinishedAt
		}
		out[i] = f
	}
	return out, nil
}

// QueueStats returns the pending and processing operations by kind and status (retain_batch parents excluded),
// ordered by (kind, status), each pending entry with the age of its kind's oldest due job by the database clock.
func (s *DB) QueueStats(ctx context.Context) ([]store.QueueStat, error) {
	rows, err := db.New(s.pool).QueueStats(ctx)
	if err != nil {
		return nil, mapErr("queue stats", err)
	}
	out := make([]store.QueueStat, len(rows))
	for i, row := range rows {
		out[i] = store.QueueStat{
			Kind: domain.OperationKind(row.Kind), Status: domain.OperationStatus(row.Status), Jobs: int(row.Jobs),
			OldestDue: time.Duration(row.OldestDueSeconds * float64(time.Second)),
		}
	}
	return out, nil
}

// SettleRetainBatches ends every PENDING retain_batch parent whose children are all terminal: FAILED when a
// child failed, else CANCELLED when a child was cancelled, else COMPLETED, with per-status counts as its
// result; it returns how many parents it settled. Parents are never claimed, so without it they would stay
// PENDING and block the BankStats barrier. It implements store.Maintenance.
func (s *DB) SettleRetainBatches(ctx context.Context) (int, error) {
	n, err := db.New(s.pool).SettleRetainBatches(ctx)
	if err != nil {
		return 0, mapErr("settle retain batches", err)
	}
	return int(n), nil
}

// StrandedFacts returns up to limit live facts whose embedding is NULL because their rederive_memory
// operation ended FAILED or CANCELLED, on banks that are not tombstoned, skipping units with a rederive
// that is pending, processing or ended less than retryAfter ago. Each unit carries only Bank, ID and
// DocumentID. It implements store.Maintenance.
func (s *DB) StrandedFacts(ctx context.Context, retryAfter time.Duration, limit int) ([]domain.Unit, error) {
	if limit <= 0 {
		return []domain.Unit{}, nil
	}
	rows, err := db.New(s.pool).StrandedFacts(ctx, db.StrandedFactsParams{
		RetryAfterSeconds: retryAfter.Seconds(), Lim: int32(min(limit, 1<<20)), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, mapErr("stranded facts", err)
	}
	out := make([]domain.Unit, len(rows))
	for i, row := range rows {
		out[i] = domain.Unit{
			ID:   idOf[domain.UnitID](row.ID),
			Bank: domain.NewBankRef(row.TenantID, row.OrgID, row.BankID),
		}
		if row.DocumentID != nil {
			out[i].DocumentID = domain.DocumentID(*row.DocumentID)
		}
	}
	return out, nil
}
