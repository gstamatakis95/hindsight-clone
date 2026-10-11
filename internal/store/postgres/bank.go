package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"

	"github.com/jackc/pgx/v5"
)

// bankRepo is store.BankRepo over one transaction. DeleteBatch and Delete live in bank_delete.go.
type bankRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.BankRepo = (*bankRepo)(nil)

// Get returns the bank, including a tombstoned one; unknown = domain.ErrNotFound.
func (r *bankRepo) Get(ctx context.Context, b domain.BankRef) (domain.Bank, error) {
	row, err := r.q.GetBank(ctx, db.GetBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return domain.Bank{}, bankErr(b, err)
	}
	return bankOf(row)
}

// Ensure returns the bank, creating it with empty overrides when missing; a tombstoned bank is
// domain.ErrBankDeleting.
func (r *bankRepo) Ensure(ctx context.Context, b domain.BankRef) (domain.Bank, error) {
	if err := r.q.InsertBankIfMissing(ctx, db.InsertBankIfMissingParams{TenantID: b.Tenant, ID: b.Bank}); err != nil {
		return domain.Bank{}, mapErr("ensure bank", err)
	}
	bank, err := r.Get(ctx, b)
	if err != nil {
		return domain.Bank{}, err
	}
	if bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w: %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	return bank, nil
}

// Create inserts a new bank with its name and config; an existing id is domain.ErrAlreadyExists.
func (r *bankRepo) Create(ctx context.Context, bank domain.Bank) error {
	cfg, err := marshal("bank config", bank.Config)
	if err != nil {
		return err
	}
	err = r.q.CreateBank(ctx, db.CreateBankParams{
		TenantID: bank.Ref.Tenant, ID: bank.Ref.Bank, Name: bank.Name, Config: cfg,
	})
	return mapErr("create bank", err)
}

// SetConfig replaces the bank's own overrides, creating the bank if needed; a tombstoned bank is
// domain.ErrBankDeleting.
func (r *bankRepo) SetConfig(ctx context.Context, b domain.BankRef, c domain.BankConfig) error {
	cfg, err := marshal("bank config", c)
	if err != nil {
		return err
	}
	n, err := r.q.UpsertBankConfig(ctx, db.UpsertBankConfigParams{TenantID: b.Tenant, ID: b.Bank, Config: cfg})
	if err != nil {
		return mapErr("set bank config", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	return nil
}

// MarkDeleting tombstones the bank; marking it again keeps the first time. Unknown = domain.ErrNotFound.
func (r *bankRepo) MarkDeleting(ctx context.Context, b domain.BankRef, at time.Time) error {
	n, err := r.q.MarkBankDeleting(ctx, db.MarkBankDeletingParams{TenantID: b.Tenant, ID: b.Bank, At: at})
	if err != nil {
		return mapErr("mark bank deleting", err)
	}
	if n == 0 {
		return fmt.Errorf("%w %s/%s", domain.ErrBankNotFound, b.Tenant, b.Bank)
	}
	return nil
}

// TenantConfig returns the tenant-level config; a tenant with none set returns the zero config.
func (r *bankRepo) TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error) {
	var cfg domain.BankConfig
	raw, err := r.q.GetTenantConfig(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, mapErr("tenant config", err)
	}
	return cfg, unmarshal("tenant config", raw, &cfg)
}

// LockTenantConfig takes the tenant config's transaction-scoped advisory lock (decision 2500).
func (r *bankRepo) LockTenantConfig(ctx context.Context, tenant string) error {
	return mapErr("lock tenant config", r.q.TenantConfigLock(ctx, tenant))
}

// SetTenantConfig replaces the tenant-level config.
func (r *bankRepo) SetTenantConfig(ctx context.Context, tenant string, c domain.BankConfig) error {
	cfg, err := marshal("tenant config", c)
	if err != nil {
		return err
	}
	return mapErr("set tenant config", r.q.UpsertTenantConfig(ctx, db.UpsertTenantConfigParams{ID: tenant, Config: cfg}))
}
