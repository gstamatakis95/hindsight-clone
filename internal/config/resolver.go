package config

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// StrategyMatcher is the part of consolidate.StrategyMatcher the resolver uses (consumer-declared, so
// config does not import the consolidate engine).
type StrategyMatcher interface {
	// Match returns the first strategy with a pattern matching s, and whether there is one.
	Match(strategies []domain.ConsolidationStrategy, s port.ScopeKey) (*domain.ConsolidationStrategy, bool)
}

// resolver is the port.ConfigResolver over the bank repository.
type resolver struct {
	store   port.Store
	matcher StrategyMatcher
	server  domain.BankConfig
}

var _ port.ConfigResolver = (*resolver)(nil)

// NewResolver returns the ConfigResolver. It reads the tenant and bank levels in one short read-only
// transaction (port.Reader has no config reads, so it takes the Store; decision 402) and merges them
// over server, the ServerDefaults level. m may be nil, which disables strategies in ForScope.
// Callers must not call it inside an open Store.InTx.
func NewResolver(s port.Store, m StrategyMatcher, server domain.BankConfig) port.ConfigResolver {
	return &resolver{store: s, matcher: m, server: Merge(server)}
}

// Effective merges server < tenant < bank, then each override in order. An unknown bank and a
// tombstoned one are domain.ErrNotFound (the read rule); write paths Ensure the bank first, which
// creates it or reports domain.ErrBankDeleting.
func (r *resolver) Effective(
	ctx context.Context, b port.BankRef, overrides ...domain.BankConfig,
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
	ctx context.Context, s port.ScopeKey, request *domain.BankConfig,
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
func (r *resolver) levels(ctx context.Context, b port.BankRef) ([]domain.BankConfig, error) {
	var tenant, bank domain.BankConfig
	err := r.store.InTx(ctx, func(tx port.Tx) error {
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
