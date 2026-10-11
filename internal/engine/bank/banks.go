package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// liveBank returns bank b for a read: unknown or tombstoned is domain.ErrNotFound (SPEC: every read of a
// deleting bank returns NOT_FOUND).
func liveBank(ctx context.Context, tx port.Tx, b port.BankRef) (domain.Bank, error) {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return domain.Bank{}, err
	}
	if bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
	}
	return bank, nil
}

// writableBank returns existing bank b for a write that must not create it: unknown is
// domain.ErrNotFound, tombstoned is domain.ErrBankDeleting.
func writableBank(ctx context.Context, tx port.Tx, b port.BankRef) (domain.Bank, error) {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return domain.Bank{}, err
	}
	if bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	return bank, nil
}

// tenantAdmin implements TenantAdmin over port.Store.
type tenantAdmin struct{ s port.Store }

var _ TenantAdmin = (*tenantAdmin)(nil)

// NewTenantAdmin returns the TenantAdmin over s.
func NewTenantAdmin(s port.Store) TenantAdmin { return &tenantAdmin{s: s} }

// TenantConfig reads the tenant's config in a short transaction.
func (a *tenantAdmin) TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error) {
	var cfg domain.BankConfig
	err := a.s.InTx(ctx, func(tx port.Tx) error {
		var err error
		cfg, err = tx.Banks().TenantConfig(ctx, tenant)
		return err
	})
	if err != nil {
		return domain.BankConfig{}, err
	}
	return cfg, nil
}

// UpdateTenantConfig reads, patches and stores the tenant config in one transaction. It locks the tenant
// config first, so concurrent patches serialize instead of one overwriting the other (decision 2500).
func (a *tenantAdmin) UpdateTenantConfig(
	ctx context.Context, tenant string, patch ConfigPatch,
) (domain.BankConfig, error) {
	var out domain.BankConfig
	err := a.s.InTx(ctx, func(tx port.Tx) error {
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

// bankAdmin implements BankAdmin over port.Store.
type bankAdmin struct{ s port.Store }

var _ BankAdmin = (*bankAdmin)(nil)

// NewBankAdmin returns the BankAdmin over s.
func NewBankAdmin(s port.Store) BankAdmin { return &bankAdmin{s: s} }

// CreateBank inserts the bank and returns it as stored. A bank being deleted is domain.ErrBankDeleting
// (FAILED_PRECONDITION: writes wait for the delete to finish); an existing one is domain.ErrAlreadyExists
// with a message of its own, never the store's (no SQL text reaches the client).
func (a *bankAdmin) CreateBank(ctx context.Context, bank domain.Bank) (domain.Bank, error) {
	var out domain.Bank
	b := bank.Ref
	exists := fmt.Errorf("%w: bank %s/%s already exists", domain.ErrAlreadyExists, b.Tenant, b.Bank)
	err := a.s.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.Banks().Get(ctx, b)
		switch {
		case err == nil && cur.Deleting():
			return fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
		case err == nil:
			return exists
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
		err = tx.Banks().Create(ctx, domain.Bank{Ref: b, Name: bank.Name, Config: bank.Config})
		if errors.Is(err, domain.ErrAlreadyExists) {
			return exists // lost a race with a concurrent create
		}
		if err != nil {
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

// GetBank returns the bank, tombstoned or not.
func (a *bankAdmin) GetBank(ctx context.Context, b port.BankRef) (domain.Bank, error) {
	var out domain.Bank
	err := a.s.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, err
	}
	return out, nil
}

// UpdateBankConfig ensures the bank, patches its own overrides and returns it, in one transaction. It takes
// the bank's change seq (which locks the bank row) before reading the overrides, so concurrent patches of
// different fields serialize instead of one overwriting the other (decision 2500).
func (a *bankAdmin) UpdateBankConfig(ctx context.Context, b port.BankRef, patch ConfigPatch) (domain.Bank, error) {
	var out domain.Bank
	err := a.s.InTx(ctx, func(tx port.Tx) error {
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
