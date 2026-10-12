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
	// Config resolves the effective config (server < tenant < org < bank < request).
	Config *config.Resolver
}

// Service serves ConfigService: the tenant, org and bank config levels, the prompt preview and the directives.
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

// View is one config level as every config RPC returns it: its own overrides and the effective config they
// resolve to.
type View struct {
	// Own is what the level itself sets: the tenant's or the org's config, or the bank's own overrides.
	Own domain.BankConfig
	// Effective is server < tenant for a tenant, server < tenant < org for an org, and server < tenant < org < bank
	// for a bank.
	Effective domain.BankConfig
}

// GetTenantConfig returns the tenant's config view; a tenant with no config of its own has a zero Own.
func (s *Service) GetTenantConfig(ctx context.Context, tenant string) (View, error) {
	var cfg domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		cfg, err = tx.Banks().TenantConfig(ctx, tenant)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return View{Own: cfg, Effective: s.d.Config.Resolve(cfg)}, nil
}

// UpdateTenantConfig reads, patches and stores the tenant config in one transaction and returns the stored result.
// It locks the tenant config first, so concurrent patches serialize instead of one overwriting the other.
func (s *Service) UpdateTenantConfig(ctx context.Context, tenant string, patch Patch) (View, error) {
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
		return View{}, err
	}
	return View{Own: out, Effective: s.d.Config.Resolve(out)}, nil
}

// ResetTenantConfig resets the given paths of the tenant's config to unset, so they inherit the server defaults
// again: UpdateTenantConfig with a reset patch. "*" resets everything; no paths at all, or an unknown path, is
// domain.ErrInvalidArgument, checked before any write.
func (s *Service) ResetTenantConfig(ctx context.Context, tenant string, paths []string) (View, error) {
	if _, err := config.ResetPaths(domain.BankConfig{}, paths); err != nil {
		return View{}, fmt.Errorf("reset tenant config: %w", err)
	}
	return s.UpdateTenantConfig(ctx, tenant, resetPatch(paths))
}

// GetOrgConfig returns the org's config view, read in one transaction; an unknown or tombstoned org is
// domain.ErrOrgNotFound.
func (s *Service) GetOrgConfig(ctx context.Context, o domain.OrgRef) (View, error) {
	var tenant, own domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		tenant, own, err = orgLevels(ctx, tx, o)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return View{Own: own, Effective: s.d.Config.Resolve(tenant, own)}, nil
}

// UpdateOrgConfig reads, patches and stores the org's config in one transaction and returns the stored result. It
// locks the org config first, so concurrent patches serialize instead of one overwriting the other; the lock and the
// update never block bank creation in the org. Orgs are never created here: an unknown or tombstoned org is
// domain.ErrOrgNotFound.
func (s *Service) UpdateOrgConfig(ctx context.Context, o domain.OrgRef, patch Patch) (View, error) {
	var tenant, own domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		tenant, own = domain.BankConfig{}, domain.BankConfig{}
		if err := tx.Orgs().LockConfig(ctx, o); err != nil {
			return err
		}
		_, cur, err := orgLevels(ctx, tx, o)
		if err != nil {
			return err
		}
		next, err := patch(cur)
		if err != nil {
			return fmt.Errorf("patch org config: %w", err)
		}
		if err := tx.Orgs().SetConfig(ctx, o, next); err != nil {
			return err
		}
		tenant, own, err = orgLevels(ctx, tx, o)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return View{Own: own, Effective: s.d.Config.Resolve(tenant, own)}, nil
}

// ResetOrgConfig resets the given paths of the org's config to unset, so they inherit the tenant and server levels
// again: UpdateOrgConfig with a reset patch. "*" resets everything; no paths at all, or an unknown path, is
// domain.ErrInvalidArgument, checked before any write.
func (s *Service) ResetOrgConfig(ctx context.Context, o domain.OrgRef, paths []string) (View, error) {
	if _, err := config.ResetPaths(domain.BankConfig{}, paths); err != nil {
		return View{}, fmt.Errorf("reset org config: %w", err)
	}
	return s.UpdateOrgConfig(ctx, o, resetPatch(paths))
}

// GetBankConfig returns the bank's config view, read in one transaction; an unknown or tombstoned org or bank is
// domain.ErrNotFound.
func (s *Service) GetBankConfig(ctx context.Context, b domain.BankRef) (View, error) {
	var tenant, org, own domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		if tenant, org, err = orgLevels(ctx, tx, b.OrgRef()); err != nil {
			return err
		}
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.Deleting() {
			return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b)
		}
		own = bank.Config
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return View{Own: own, Effective: s.d.Config.Resolve(tenant, org, own)}, nil
}

// UpdateBankConfig ensures the bank, patches its own overrides and returns its config view, in one transaction.
// It is a write: an unknown bank is created with empty overrides in its existing org (BankRepo.Ensure; an unknown
// org is domain.ErrOrgNotFound), a tombstoned one is domain.ErrBankDeleting. It takes the bank's change seq (which
// locks the bank row) before reading the overrides,
// so concurrent patches of different fields serialize instead of one overwriting the other.
func (s *Service) UpdateBankConfig(ctx context.Context, b domain.BankRef, patch Patch) (View, error) {
	var tenant, org, own domain.BankConfig
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		tenant, org, own = domain.BankConfig{}, domain.BankConfig{}, domain.BankConfig{}
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
		stored, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		own = stored.Config
		tenant, org, err = orgLevels(ctx, tx, b.OrgRef())
		return err
	})
	if err != nil {
		return View{}, err
	}
	return View{Own: own, Effective: s.d.Config.Resolve(tenant, org, own)}, nil
}

// ResetBankConfig resets the given paths of the bank's own overrides to unset, so they inherit again, and returns
// its config view: UpdateBankConfig with a reset patch. "*" resets everything; no paths at all, or an unknown path,
// is domain.ErrInvalidArgument, checked before any write.
func (s *Service) ResetBankConfig(ctx context.Context, b domain.BankRef, paths []string) (View, error) {
	if _, err := config.ResetPaths(domain.BankConfig{}, paths); err != nil {
		return View{}, fmt.Errorf("reset bank config: %w", err)
	}
	return s.UpdateBankConfig(ctx, b, resetPatch(paths))
}

// orgLevels reads the tenant and org config levels of o in tx (config.StoredOrgLevels).
func orgLevels(ctx context.Context, tx store.Tx, o domain.OrgRef) (tenant, org domain.BankConfig, err error) {
	if tenant, org, err = config.StoredOrgLevels(ctx, tx, o); err != nil {
		return tenant, org, fmt.Errorf("org config: %w", err)
	}
	return tenant, org, nil
}

// resetPatch is the Patch that unsets paths.
func resetPatch(paths []string) Patch {
	return func(cur domain.BankConfig) (domain.BankConfig, error) {
		return config.ResetPaths(cur, paths) //nolint:wrapcheck // config errors are already domain error classes
	}
}
