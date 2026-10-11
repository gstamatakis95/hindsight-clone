package config

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// StrategyMatcher is the part of consolidate.StrategyMatcher the resolver uses (consumer-declared, so
// config does not import the consolidate engine).
type StrategyMatcher interface {
	// Match returns the first strategy with a pattern matching s, and whether there is one.
	Match(strategies []domain.ConsolidationStrategy, s domain.ScopeKey) (*domain.ConsolidationStrategy, bool)
}

// Resolver merges server < tenant < bank < strategy < request into the effective config.
type Resolver interface {
	// Effective merges server, tenant and bank config, then each override in order.
	Effective(ctx context.Context, b domain.BankRef, overrides ...domain.BankConfig) (domain.BankConfig, error)
	// ForScope also slots the scope's matching consolidation strategy in below the request level.
	ForScope(ctx context.Context, s domain.ScopeKey, request *domain.BankConfig) (domain.BankConfig, error)
}

// resolver is the Resolver over the bank repository.
type resolver struct {
	store   store.DB
	matcher StrategyMatcher
	server  domain.BankConfig
}

var _ Resolver = (*resolver)(nil)

// NewResolver returns the Resolver. It reads the tenant and bank levels in one short read-only
// transaction (store.Reader has no config reads, so it takes the DB) and merges them
// over server, the ServerDefaults level. m may be nil, which disables strategies in ForScope.
// Callers must not call it inside an open DB.InTx.
func NewResolver(s store.DB, m StrategyMatcher, server domain.BankConfig) Resolver {
	return &resolver{store: s, matcher: m, server: Merge(server)}
}

// Effective merges server < tenant < bank, then each override in order. An unknown bank and a
// tombstoned one are domain.ErrNotFound (the read rule); write paths Ensure the bank first, which
// creates it or reports domain.ErrBankDeleting.
func (r *resolver) Effective(
	ctx context.Context, b domain.BankRef, overrides ...domain.BankConfig,
) (domain.BankConfig, error) {
	levels, err := r.levels(ctx, b)
	if err != nil {
		return domain.BankConfig{}, err
	}
	return Merge(append(levels, overrides...)...), nil
}

// ForScope merges server < tenant < bank < strategy < request, where strategy is the first consolidation
// strategy matching s. The strategy list itself is the merged one, request included, so a Consolidate
// call can bring its own strategies.
func (r *resolver) ForScope(
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
	if r.matcher != nil {
		strategies := Merge(append(levels, req...)...).ConsolidationStrategies
		if st, ok := r.matcher.Match(strategies, s); ok && st != nil {
			levels = append(levels, StrategyLevel(*st))
		}
	}
	return Merge(append(levels, req...)...), nil
}

// levels reads the tenant and bank levels and returns server, tenant, bank.
func (r *resolver) levels(ctx context.Context, b domain.BankRef) ([]domain.BankConfig, error) {
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
			return fmt.Errorf("%w: bank %s is being deleted", domain.ErrNotFound, b.Bank)
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
	return Merge(domain.BankConfig{
		ObservationsMission:               s.ObservationsMission,
		MaxObservationsPerScope:           s.MaxObservationsPerScope,
		ConsolidationSourceFactsMaxTokens: s.SourceFactsMaxTokens,
		ConsolidationSourceTokensPerObs:   s.SourceFactsMaxTokensPerObservation,
		Models:                            s.Models,
	})
}
