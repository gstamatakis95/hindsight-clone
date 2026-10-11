package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/port"
	"time"
)

// Maintenance-tick reads. None is a port method: the worker's maintenance tick consumes each through its own
// interface (worker.ConsolidationCandidates, worker.StalledDeleteSource, worker.GraphWorkSource), narrowed in
// cmd/memd/wire.go, as decisions 1452 and 1453 did (decisions 2000-2002).

// UnconsolidatedBanks returns, ordered by (tenant, bank), every bank that is not tombstoned and holds an
// unconsolidated fact with an embedding created before before (decision 2000).
func (s *Store) UnconsolidatedBanks(ctx context.Context, before time.Time) ([]port.BankRef, error) {
	rows, err := db.New(s.pool).UnconsolidatedBanks(ctx, before)
	if err != nil {
		return nil, mapErr("unconsolidated banks", err)
	}
	out := make([]port.BankRef, len(rows))
	for i, row := range rows {
		out[i] = port.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}

// StalledBankDeletes returns, ordered by (tenant, bank), every tombstoned bank with no pending or processing
// delete_bank operation (decision 2001).
func (s *Store) StalledBankDeletes(ctx context.Context) ([]port.BankRef, error) {
	rows, err := db.New(s.pool).StalledBankDeletes(ctx)
	if err != nil {
		return nil, mapErr("stalled bank deletes", err)
	}
	out := make([]port.BankRef, len(rows))
	for i, row := range rows {
		out[i] = port.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}

// BanksWithGraphWork returns, ordered by (tenant, bank), every bank that is not tombstoned and has
// graph_dirty or entity_maintenance rows queued (decision 2002).
func (s *Store) BanksWithGraphWork(ctx context.Context) ([]port.BankRef, error) {
	rows, err := db.New(s.pool).BanksWithGraphWork(ctx)
	if err != nil {
		return nil, mapErr("banks with graph work", err)
	}
	out := make([]port.BankRef, len(rows))
	for i, row := range rows {
		out[i] = port.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}
