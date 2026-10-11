package configsvc

import (
	"context"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/store"
)

// Deps are the Service's dependencies; every field is required.
type Deps struct {
	// DB reads and writes the config levels and directives in short transactions.
	DB store.DB
	// Config resolves the effective config (server < tenant < bank < request).
	Config *config.Resolver
}

// Service serves TenantService and the config, directive and prompt-preview RPCs of BankService.
type Service struct {
	d Deps
}

// New returns the Service over d.
func New(d Deps) *Service {
	return &Service{d: d}
}

// Patch computes a new config level from the current one. It must be pure: it runs inside the write transaction
// and may run again on a retry, so a concurrent patch is never lost.
type Patch func(cur domain.BankConfig) (domain.BankConfig, error)

// View is a bank's config: its own overrides and the effective config resolved over every level.
type View struct {
	// Own is the bank's own overrides.
	Own domain.BankConfig
	// Effective is the config the bank's operations use: server < tenant < bank.
	Effective domain.BankConfig
}

// GetTenantConfig returns the tenant's config; a tenant with none set returns the zero config.
func (s *Service) GetTenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error) {
	var cfg domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		cfg, err = tx.Banks().TenantConfig(ctx, tenant)
		return err
	})
	if err != nil {
		return domain.BankConfig{}, err
	}
	return cfg, nil
}

// UpdateTenantConfig reads, patches and stores the tenant config in one transaction and returns the stored result.
// It locks the tenant config first, so concurrent patches serialize instead of one overwriting the other.
func (s *Service) UpdateTenantConfig(ctx context.Context, tenant string, patch Patch) (domain.BankConfig, error) {
	var out domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = domain.BankConfig{}
		if err := tx.Banks().LockTenantConfig(ctx, tenant); err != nil {
			return err
		}
		cur, err := tx.Banks().TenantConfig(ctx, tenant)
		if err != nil {
			return err
		}
		next, err := patch(cur)
		if err != nil {
			return fmt.Errorf("patch tenant config: %w", err)
		}
		if err := tx.Banks().SetTenantConfig(ctx, tenant, next); err != nil {
			return err
		}
		out, err = tx.Banks().TenantConfig(ctx, tenant)
		return err
	})
	if err != nil {
		return domain.BankConfig{}, err
	}
	return out, nil
}

// GetBankConfig returns the bank's own overrides and its effective config; an unknown or tombstoned bank is
// domain.ErrNotFound (the effective config refuses a tombstoned bank).
func (s *Service) GetBankConfig(ctx context.Context, b domain.BankRef) (View, error) {
	var bank domain.Bank
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		bank, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return View{}, err
	}
	eff, err := s.d.Config.Effective(ctx, b)
	if err != nil {
		return View{}, fmt.Errorf("effective config: %w", err)
	}
	return View{Own: bank.Config, Effective: eff}, nil
}

// UpdateBankConfig ensures the bank, patches its own overrides and returns the bank, in one transaction. It is a
// write: an unknown bank is created with tenant defaults (BankRepo.Ensure), a tombstoned one is
// domain.ErrBankDeleting. It takes the bank's change seq (which locks the bank row) before reading the overrides,
// so concurrent patches of different fields serialize instead of one overwriting the other.
func (s *Service) UpdateBankConfig(ctx context.Context, b domain.BankRef, patch Patch) (domain.Bank, error) {
	var out domain.Bank
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = domain.Bank{}
		if _, err := tx.Banks().Ensure(ctx, b); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		cur, err := tx.Banks().Get(ctx, b) // re-read under the lock: Ensure's read may predate it
		if err != nil {
			return err
		}
		next, err := patch(cur.Config)
		if err != nil {
			return fmt.Errorf("patch bank config: %w", err)
		}
		if err := tx.Banks().SetConfig(ctx, b, next); err != nil {
			return err
		}
		out, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, err
	}
	return out, nil
}

// ResetBankConfig resets the given paths of the bank's own overrides to unset, so they inherit again, and returns
// the bank: UpdateBankConfig with a reset patch. "*" resets everything; no paths at all, or an unknown path, is
// domain.ErrInvalidArgument, checked before any write.
func (s *Service) ResetBankConfig(ctx context.Context, b domain.BankRef, paths []string) (domain.Bank, error) {
	if _, err := config.ResetPaths(domain.BankConfig{}, paths); err != nil {
		return domain.Bank{}, fmt.Errorf("reset bank config: %w", err)
	}
	return s.UpdateBankConfig(ctx, b, func(cur domain.BankConfig) (domain.BankConfig, error) {
		return config.ResetPaths(cur, paths) //nolint:wrapcheck // config errors are already domain error classes
	})
}
