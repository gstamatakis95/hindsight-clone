package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// ConfigPatch computes a new config from the current one. It must be pure: it runs inside the write
// transaction and may run again on a retry, so a concurrent patch is never lost. It is the type
// server.ConfigPatch becomes an alias of (decision 1302).
type ConfigPatch func(cur domain.BankConfig) (domain.BankConfig, error)

// TenantAdmin reads and patches the tenant-level config.
type TenantAdmin interface {
	// TenantConfig returns the tenant's config; a tenant with none set returns the zero config.
	TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error)
	// UpdateTenantConfig applies patch to the stored tenant config in one transaction and returns the
	// stored result.
	UpdateTenantConfig(ctx context.Context, tenant string, patch ConfigPatch) (domain.BankConfig, error)
}

// BankAdmin creates and reads banks and patches their own overrides.
type BankAdmin interface {
	// CreateBank inserts bank (Ref, Name, Config) and returns it as stored; an existing id is
	// domain.ErrAlreadyExists.
	CreateBank(ctx context.Context, bank domain.Bank) (domain.Bank, error)
	// GetBank returns the bank, a tombstoned one included (DeletingAt set); unknown = domain.ErrNotFound.
	GetBank(ctx context.Context, b port.BankRef) (domain.Bank, error)
	// UpdateBankConfig applies patch to the bank's own overrides in one transaction and returns the bank.
	// It is a write: an unknown bank is created with tenant defaults (BankRepo.Ensure), a tombstoned one is
	// domain.ErrBankDeleting.
	UpdateBankConfig(ctx context.Context, b port.BankRef, patch ConfigPatch) (domain.Bank, error)
}
