package config

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"reflect"
	"sync"
	"testing"
	"time"

	mockconsolidate "memd/internal/mocks/engine/consolidate"
	mockport "memd/internal/mocks/port"
)

var ref = port.BankRef{Tenant: "t1", Bank: "b1"}

// fakeStore wires the generated mocks: one tenant config and a set of banks.
func fakeStore(tenant domain.BankConfig, banks map[string]domain.Bank) *mockport.Store {
	repo := &mockport.BankRepo{
		TenantConfigFunc: func(_ context.Context, _ string) (domain.BankConfig, error) { return tenant, nil },
		GetFunc: func(_ context.Context, b port.BankRef) (domain.Bank, error) {
			bk, ok := banks[b.Bank]
			if !ok {
				return domain.Bank{}, domain.ErrNotFound
			}
			return bk, nil
		},
	}
	tx := &mockport.Tx{BanksFunc: func() port.BankRepo { return repo }}
	return &mockport.Store{InTxFunc: func(_ context.Context, fn func(port.Tx) error) error { return fn(tx) }}
}

func TestEffectivePrecedence(t *testing.T) {
	server := domain.BankConfig{
		RetainMission: "server", ReflectMission: "server", ObservationsMission: "server",
		Models: []domain.SlotBinding{bind(domain.ModelSlotExtract, "s-ext"), bind(domain.ModelSlotRerank, "s-rr")},
	}
	tenant := domain.BankConfig{ReflectMission: "tenant", ObservationsMission: "tenant"}
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{
		ObservationsMission: "bank",
		Models:              []domain.SlotBinding{bind(domain.ModelSlotRerank, "b-rr")},
	}}
	r := NewResolver(fakeStore(tenant, map[string]domain.Bank{"b1": bank}), nil, server)

	got, err := r.Effective(context.Background(), ref,
		domain.BankConfig{DedupThreshold: ptr(0.5)}, domain.BankConfig{DedupThreshold: ptr(0.6)})
	if err != nil {
		t.Fatal(err)
	}
	if got.RetainMission != "server" || got.ReflectMission != "tenant" || got.ObservationsMission != "bank" ||
		*got.DedupThreshold != 0.6 || modelOf(t, got, domain.ModelSlotRerank) != "b-rr" ||
		modelOf(t, got, domain.ModelSlotExtract) != "s-ext" {
		t.Fatalf("got %+v", got)
	}
}

func TestEffectiveUnknownOrDeletingBank(t *testing.T) {
	now := time.Unix(0, 0)
	banks := map[string]domain.Bank{"gone": {Ref: port.BankRef{Tenant: "t1", Bank: "gone"}, DeletingAt: &now}}
	r := NewResolver(fakeStore(domain.BankConfig{}, banks), nil, domain.BankConfig{})
	for _, b := range []string{"missing", "gone"} {
		_, err := r.Effective(context.Background(), port.BankRef{Tenant: "t1", Bank: b})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want NotFound", b, err)
		}
	}
}

func TestForScopeStrategyBetweenBankAndRequest(t *testing.T) {
	strat := domain.ConsolidationStrategy{
		Name:                    "people",
		ObservationsMission:     "strategy",
		MaxObservationsPerScope: ptr(7),
		SourceFactsMaxTokens:    ptr(11),
		Models:                  []domain.SlotBinding{bind(domain.ModelSlotConsolidate, "st-con")},
	}
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{
		ObservationsMission:     "bank",
		MaxObservationsPerScope: ptr(3),
		ConsolidationStrategies: []domain.ConsolidationStrategy{strat},
		Models: []domain.SlotBinding{
			bind(domain.ModelSlotConsolidate, "b-con"), bind(domain.ModelSlotDedupCheck, "b-dd"),
		},
	}}
	var gotStrategies []domain.ConsolidationStrategy
	m := &mockconsolidate.StrategyMatcher{MatchFunc: func(
		s []domain.ConsolidationStrategy, _ port.ScopeKey,
	) (*domain.ConsolidationStrategy, bool) {
		gotStrategies = s
		if len(s) == 0 {
			return nil, false
		}
		return &s[0], true
	}}
	r := NewResolver(fakeStore(domain.BankConfig{}, map[string]domain.Bank{"b1": bank}), m, domain.BankConfig{})
	scope := domain.NewScopeKey(ref, []string{"people"})

	got, err := r.ForScope(context.Background(), scope, &domain.BankConfig{MaxObservationsPerScope: ptr(9)})
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationsMission != "strategy" || *got.MaxObservationsPerScope != 9 ||
		*got.ConsolidationSourceFactsMaxTokens != 11 || modelOf(t, got, domain.ModelSlotConsolidate) != "st-con" ||
		modelOf(t, got, domain.ModelSlotDedupCheck) != "b-dd" {
		t.Fatalf("got %+v", got)
	}
	if len(gotStrategies) != 1 || gotStrategies[0].Name != "people" {
		t.Fatalf("matcher saw %+v", gotStrategies)
	}

	// A request's own strategies replace the bank's list before matching.
	reqStrat := domain.ConsolidationStrategy{Name: "req", ObservationsMission: "request strategy"}
	got, err = r.ForScope(context.Background(), scope,
		&domain.BankConfig{ConsolidationStrategies: []domain.ConsolidationStrategy{reqStrat}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationsMission != "request strategy" {
		t.Fatalf("observations_mission = %q", got.ObservationsMission)
	}

	// No match and no request: bank level only.
	m.MatchFunc = func([]domain.ConsolidationStrategy, port.ScopeKey) (*domain.ConsolidationStrategy, bool) {
		return nil, false
	}
	got, err = r.ForScope(context.Background(), scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationsMission != "bank" || modelOf(t, got, domain.ModelSlotConsolidate) != "b-con" {
		t.Fatalf("got %+v", got)
	}
}

// TestEffectiveConcurrentNoAliasing runs Effective concurrently under -race while mutating results, and
// checks the server level the resolver holds is never changed.
func TestEffectiveConcurrentNoAliasing(t *testing.T) {
	server, err := ServerDefaults(func(k string) string {
		if k == EnvModel {
			return "m"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := Merge(server)
	bank := domain.Bank{Ref: ref, Config: domain.BankConfig{Recall: &domain.RecallConfig{RRFK: ptr(5)}}}
	r := NewResolver(fakeStore(domain.BankConfig{}, map[string]domain.Bank{"b1": bank}), nil, server)
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
			got.Models[0].Binding.Model = "changed"
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
