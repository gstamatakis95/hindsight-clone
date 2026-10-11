package config

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"reflect"
	"sync"
	"testing"
	"time"
)

var ref = domain.BankRef{Tenant: "t1", Bank: "b1"}

// seedStore returns a store over a new database holding tenant t1's config and banks (with their configs,
// tombstoned when DeletingAt is set).
func seedStore(t *testing.T, tenant domain.BankConfig, banks map[string]domain.Bank) store.DB {
	t.Helper()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	err := s.InTx(ctx, func(tx store.Tx) error {
		if err := tx.Banks().SetTenantConfig(ctx, ref.Tenant, tenant); err != nil {
			return err
		}
		for _, b := range banks {
			if _, err := tx.Banks().Ensure(ctx, b.Ref); err != nil {
				return err
			}
			if err := tx.Banks().SetConfig(ctx, b.Ref, b.Config); err != nil {
				return err
			}
			if b.DeletingAt != nil {
				if err := tx.Banks().MarkDeleting(ctx, b.Ref, *b.DeletingAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEffectivePrecedence(t *testing.T) {
	server := domain.BankConfig{
		Retain:       &domain.RetainConfig{Mission: "server"},
		Reflect:      &domain.ReflectConfig{Mission: "server"},
		Observations: &domain.ObservationConfig{Mission: "server"},
		Models: bindings("", map[domain.ModelSlot]string{
			domain.ModelSlotExtract: "s-ext", domain.ModelSlotRerank: "s-rr",
		}),
	}
	tenant := domain.BankConfig{
		Reflect: &domain.ReflectConfig{Mission: "tenant"}, Observations: &domain.ObservationConfig{Mission: "tenant"},
	}
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{
		Observations: &domain.ObservationConfig{Mission: "bank"},
		Models:       bindings("", map[domain.ModelSlot]string{domain.ModelSlotRerank: "b-rr"}),
	}}
	r := NewResolver(seedStore(t, tenant, map[string]domain.Bank{"b1": bank}), server)

	got, err := r.Effective(context.Background(), ref,
		domain.BankConfig{Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.5)}},
		domain.BankConfig{Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.6)}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Retain.Mission != "server" || got.Reflect.Mission != "tenant" || got.Observations.Mission != "bank" ||
		*got.Observations.DedupThreshold != 0.6 || modelOf(t, got, domain.ModelSlotRerank) != "b-rr" ||
		modelOf(t, got, domain.ModelSlotExtract) != "s-ext" {
		t.Fatalf("got %+v", got)
	}
}

func TestEffectiveUnknownOrDeletingBank(t *testing.T) {
	now := time.Unix(0, 0)
	banks := map[string]domain.Bank{"gone": {Ref: domain.BankRef{Tenant: "t1", Bank: "gone"}, DeletingAt: &now}}
	r := NewResolver(seedStore(t, domain.BankConfig{}, banks), domain.BankConfig{})
	for _, b := range []string{"missing", "gone"} {
		_, err := r.Effective(context.Background(), domain.BankRef{Tenant: "t1", Bank: b})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want NotFound", b, err)
		}
	}
}

func TestForScopeStrategyBetweenBankAndRequest(t *testing.T) {
	strat := domain.ConsolidationStrategy{
		Name:                    "people",
		Scopes:                  []domain.ScopePattern{{Tags: []string{"people"}}},
		Mission:                 "strategy",
		MaxObservationsPerScope: ptr(7),
		SourceFactsMaxTokens:    ptr(11),
		ConsolidateModel:        &domain.ModelBinding{Model: "st-con"},
	}
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{
		Observations: &domain.ObservationConfig{
			Mission: "bank", MaxObservationsPerScope: ptr(3), Strategies: []domain.ConsolidationStrategy{strat},
		},
		Models: bindings("", map[domain.ModelSlot]string{
			domain.ModelSlotConsolidate: "b-con", domain.ModelSlotDedupCheck: "b-dd",
		}),
	}}
	r := NewResolver(seedStore(t, domain.BankConfig{}, map[string]domain.Bank{"b1": bank}), domain.BankConfig{})
	scope := domain.NewScopeKey(ref, []string{"people"})

	got, err := r.ForScope(context.Background(), scope,
		&domain.BankConfig{Observations: &domain.ObservationConfig{MaxObservationsPerScope: ptr(9)}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Observations.Mission != "strategy" || *got.Observations.MaxObservationsPerScope != 9 ||
		*got.Observations.SourceFactsMaxTokens != 11 || modelOf(t, got, domain.ModelSlotConsolidate) != "st-con" ||
		modelOf(t, got, domain.ModelSlotDedupCheck) != "b-dd" {
		t.Fatalf("got %+v", got)
	}
	// A request's own strategies replace the bank's list before matching.
	reqStrat := domain.ConsolidationStrategy{
		Name: "req", Scopes: []domain.ScopePattern{{Tags: []string{"people"}}}, Mission: "request strategy",
	}
	got, err = r.ForScope(context.Background(), scope, &domain.BankConfig{
		Observations: &domain.ObservationConfig{Strategies: []domain.ConsolidationStrategy{reqStrat}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Observations.Mission != "request strategy" {
		t.Fatalf("observations.mission = %q", got.Observations.Mission)
	}

	// No match and no request: bank level only.
	got, err = r.ForScope(context.Background(), domain.NewScopeKey(ref, []string{"other"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Observations.Mission != "bank" || modelOf(t, got, domain.ModelSlotConsolidate) != "b-con" {
		t.Fatalf("got %+v", got)
	}
}

// TestEffectiveConcurrentNoAliasing runs Effective concurrently under -race while mutating results, and
// checks the server level the resolver holds is never changed.
func TestEffectiveConcurrentNoAliasing(t *testing.T) {
	server, err := ServerDefaults(domain.BankConfig{Models: bindings("m", nil)})
	if err != nil {
		t.Fatal(err)
	}
	snap := Merge(server)
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{Recall: &domain.RecallConfig{RRFK: ptr(5)}}}
	r := NewResolver(seedStore(t, domain.BankConfig{}, map[string]domain.Bank{"b1": bank}), server)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := r.Effective(context.Background(), ref, domain.BankConfig{Reflect: &domain.ReflectConfig{}})
			if err != nil {
				t.Error(err)
				return
			}
			*got.Recall.BudgetLow = 1
			*got.Reflect.MaxIterations = 1
			got.Models.Extract.Model = "changed"
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(server, snap) {
		t.Fatal("server defaults were mutated through a result")
	}
	got, _ := r.Effective(context.Background(), ref)
	if *got.Recall.BudgetLow != 100 || *got.Recall.RRFK != 5 || modelOf(t, got, domain.ModelSlotExtract) != "m" {
		t.Fatalf("resolver state leaked: %+v", got)
	}
}
