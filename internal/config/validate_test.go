package config

import (
	"errors"
	"memd/internal/domain"
	"testing"
)

func TestValidate(t *testing.T) {
	strategies := func(names ...string) domain.BankConfig {
		o := &domain.ObservationConfig{}
		for _, n := range names {
			o.Strategies = append(o.Strategies, domain.ConsolidationStrategy{Name: n})
		}
		return domain.BankConfig{Observations: o}
	}
	tests := []struct {
		name   string
		cfg    domain.BankConfig
		server bool // ValidateServer accepts it
		level  bool // Validate accepts it
	}{
		{"empty", domain.BankConfig{}, true, true},
		{"disposition in range", domain.BankConfig{Reflect: &domain.ReflectConfig{
			Disposition: &domain.Disposition{Skepticism: 5, Empathy: 1},
		}}, true, true},
		{"disposition out of range", domain.BankConfig{Reflect: &domain.ReflectConfig{
			Disposition: &domain.Disposition{Literalism: 6},
		}}, false, false},
		{"unique strategy names", strategies("a", "b"), true, true},
		{"duplicate strategy names", strategies("a", "a"), false, false},
		{"binding without a model", domain.BankConfig{Models: &domain.ModelBindings{
			Extract: &domain.ModelBinding{},
		}}, false, false},
		{
			"strategy binding without a model",
			domain.BankConfig{Observations: &domain.ObservationConfig{
				Strategies: []domain.ConsolidationStrategy{{Name: "s", DedupCheckModel: &domain.ModelBinding{}}},
			}},
			false, false,
		},
		{
			"embed",
			domain.BankConfig{Models: &domain.ModelBindings{Embed: &domain.ModelBinding{Model: "e"}}},
			true, false,
		},
		{"negative grace", domain.BankConfig{Observations: &domain.ObservationConfig{
			ReconcileGraceSeconds: ptr(-1),
		}}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct {
				check func(domain.BankConfig) error
				ok    bool
			}{{ValidateServer, tc.server}, {Validate, tc.level}} {
				err := c.check(tc.cfg)
				if c.ok && err != nil || !c.ok && !errors.Is(err, domain.ErrInvalidArgument) {
					t.Errorf("err = %v, want ok = %v", err, c.ok)
				}
			}
		})
	}
}

func TestCheckArms(t *testing.T) {
	registered := []string{"vector", "text"}
	recall := func(arms []string, boosts map[string]float64) domain.BankConfig {
		return domain.BankConfig{Recall: &domain.RecallConfig{Arms: arms, ArmBoosts: boosts}}
	}
	for _, tc := range []struct {
		name string
		cfg  domain.BankConfig
		ok   bool
	}{
		{"no recall group", domain.BankConfig{}, true},
		{"registered names", recall([]string{"text", "vector"}, map[string]float64{"vector": 2}), true},
		{"unknown arm", recall([]string{"semantic"}, nil), false},
		{"repeated arm", recall([]string{"text", "text"}, nil), false},
		{"unknown boost", recall(nil, map[string]float64{"graph": 2}), false},
	} {
		err := CheckArms(tc.cfg, registered)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ok = %v", tc.name, err, tc.ok)
		}
	}
}
