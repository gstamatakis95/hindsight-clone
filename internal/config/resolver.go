package config

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Resolver merges server < tenant < bank < strategy < request into the effective config. It reads the tenant and
// bank levels in one short read-only transaction, so callers must not call it inside an open DB.InTx.
type Resolver struct {
	store  store.DB
	server domain.BankConfig
}

// NewResolver returns the Resolver over s (store.Reader has no config reads, so it takes the DB), merging the
// tenant and bank levels over server, the ServerDefaults level.
func NewResolver(s store.DB, server domain.BankConfig) *Resolver {
	return &Resolver{store: s, server: Merge(server)}
}

// Effective merges server < tenant < bank, then each override in order. An unknown bank and a
// tombstoned one are domain.ErrNotFound (the read rule); write paths Ensure the bank first, which
// creates it or reports domain.ErrBankDeleting.
func (r *Resolver) Effective(
	ctx context.Context, b domain.BankRef, overrides ...domain.BankConfig,
) (domain.BankConfig, error) {
	levels, err := r.levels(ctx, b)
	if err != nil {
		return domain.BankConfig{}, err
	}
	return Merge(append(levels, overrides...)...), nil
}

// ForScope merges server < tenant < bank < strategy < request, where strategy is the first consolidation
// strategy matching s (MatchStrategy). The strategy list itself is the merged one, request included, so a Consolidate
// call can bring its own strategies.
func (r *Resolver) ForScope(
	ctx context.Context, s domain.ScopeKey, request *domain.BankConfig,
) (domain.BankConfig, error) {
	levels, err := r.levels(ctx, s.BankRef)
	if err != nil {
		return domain.BankConfig{}, err
	}
	var req []domain.BankConfig
	if request != nil {
		req = append(req, *request)
	}
	var strategies []domain.ConsolidationStrategy
	if o := Merge(append(levels, req...)...).Observations; o != nil {
		strategies = o.Strategies
	}
	if st, ok := MatchStrategy(strategies, s); ok {
		levels = append(levels, StrategyLevel(*st))
	}
	return Merge(append(levels, req...)...), nil
}

// levels reads the tenant and bank levels and returns server, tenant, bank.
func (r *Resolver) levels(ctx context.Context, b domain.BankRef) ([]domain.BankConfig, error) {
	var tenant, bank domain.BankConfig
	err := r.store.InTx(ctx, func(tx store.Tx) error {
		var err error
		if tenant, err = tx.Banks().TenantConfig(ctx, b.Tenant); err != nil {
			return err
		}
		bk, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bk.Deleting() {
			return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b.Bank)
		}
		bank = bk.Config
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("config: resolve %s/%s: %w", b.Tenant, b.Bank, err)
	}
	return []domain.BankConfig{r.server, tenant, bank}, nil
}

// StrategyLevel turns a consolidation strategy into its config level (between bank and request).
func StrategyLevel(s domain.ConsolidationStrategy) domain.BankConfig {
	lvl := domain.BankConfig{Observations: &domain.ObservationConfig{
		Mission:                            s.Mission,
		MaxObservationsPerScope:            s.MaxObservationsPerScope,
		SourceFactsMaxTokens:               s.SourceFactsMaxTokens,
		SourceFactsMaxTokensPerObservation: s.SourceFactsMaxTokensPerObservation,
	}}
	if s.ConsolidateModel != nil || s.DedupCheckModel != nil {
		lvl.Models = &domain.ModelBindings{Consolidate: s.ConsolidateModel, DedupCheck: s.DedupCheckModel}
	}
	return Merge(lvl)
}
