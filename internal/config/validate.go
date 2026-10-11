package config

import (
	"errors"
	"fmt"
	"maps"
	"memd/internal/domain"
	"slices"
	"strings"
)

// Validate checks one config level below the server: a tenant or bank config, a bank template, a request's
// overrides or a stored spec's overrides. On top of ValidateServer's rules it rejects models.embed, because
// every stored embedding shares one vector space, so the embedding model is chosen once, in the server config
// file. Failures are domain.ErrInvalidArgument naming the BankConfig path (domain.InvalidField).
func Validate(c domain.BankConfig) error {
	if c.Models != nil && c.Models.Embed != nil {
		return domain.InvalidField("models.embed", "models.embed can only be set in the server config file")
	}
	return ValidateServer(c)
}

// ValidateServer checks a config level, the server level included: each disposition trait in 0..5 (0 = unset),
// consolidation strategy names unique and their scope patterns well formed, a reconcile grace that is not
// negative, and every set model binding naming a model. Failures are domain.ErrInvalidArgument naming each
// offending BankConfig path (domain.InvalidField), joined.
func ValidateServer(c domain.BankConfig) error {
	var errs []error
	if c.Reflect != nil {
		errs = append(errs, checkDisposition(c.Reflect.Disposition))
	}
	if c.Models != nil {
		errs = append(errs, checkBinding("models.default", c.Models.Default))
		for _, s := range append(append([]domain.ModelSlot{}, domain.LLMSlots...), domain.ModelSlotRerank,
			domain.ModelSlotEmbed) {
			errs = append(errs, checkBinding("models."+string(s), *c.Models.Slot(s)))
		}
	}
	if c.Observations != nil {
		errs = append(errs, checkStrategies(c.Observations.Strategies))
		if g := c.Observations.ReconcileGraceSeconds; g != nil && *g < 0 {
			errs = append(errs, domain.InvalidField("observations.reconcile_grace_seconds",
				"observations.reconcile_grace_seconds = %d, want >= 0", *g))
		}
	}
	return errors.Join(errs...)
}

// CheckArms checks a config level's recall arm names against the arms the server registered (the wiring passes
// recall.Engine's names): every name in recall.arms must be registered and listed once, and every recall.arm_boosts
// key must be registered. Failures are domain.ErrInvalidArgument naming the path.
func CheckArms(c domain.BankConfig, registered []string) error {
	if c.Recall == nil {
		return nil
	}
	known := func(n string) bool { return slices.Contains(registered, n) }
	seen := map[string]bool{}
	for i, n := range c.Recall.Arms {
		path := fmt.Sprintf("recall.arms[%d]", i)
		switch {
		case !known(n):
			return unknownArm(path, n, registered)
		case seen[n]:
			return domain.InvalidField(path, "%s: arm %q is listed twice", path, n)
		}
		seen[n] = true
	}
	for _, n := range slices.Sorted(maps.Keys(c.Recall.ArmBoosts)) {
		if !known(n) {
			return unknownArm("recall.arm_boosts", n, registered)
		}
	}
	return nil
}

// unknownArm is the error for an unregistered arm name at path.
func unknownArm(path, name string, registered []string) error {
	return domain.InvalidField(path, "%s: unknown recall arm %q (registered: %s)", path, name,
		strings.Join(registered, ", "))
}

// checkDisposition requires each trait in 0..5 (0 = unset, inherit; 1-5 per the spec).
func checkDisposition(d *domain.Disposition) error {
	if d == nil {
		return nil
	}
	for _, t := range []struct {
		name string
		v    int
	}{{"skepticism", d.Skepticism}, {"literalism", d.Literalism}, {"empathy", d.Empathy}} {
		if t.v < 0 || t.v > 5 {
			path := "reflect.disposition." + t.name
			return domain.InvalidField(path, "%s = %d, want 1-5 (0 = unset)", path, t.v)
		}
	}
	return nil
}

// checkBinding requires a set binding to name a model.
func checkBinding(path string, b *domain.ModelBinding) error {
	if b != nil && b.Model == "" {
		return domain.InvalidField(path+".model", "%s: model is required", path)
	}
	return nil
}

// checkStrategies requires strategy names to be unique, each scope pattern to be a valid tag filter (flat tags
// or groups, not both), and each strategy's bindings to name a model.
func checkStrategies(strategies []domain.ConsolidationStrategy) error {
	seen := map[string]bool{}
	for i, s := range strategies {
		at := fmt.Sprintf("observations.strategies[%d]", i)
		if s.Name != "" && seen[s.Name] {
			return domain.InvalidField(at+".name", "%s: name %q is used twice", at, s.Name)
		}
		seen[s.Name] = true
		for j, p := range s.Scopes {
			if err := p.Validate(); err != nil {
				path := fmt.Sprintf("%s.scopes[%d]", at, j)
				return domain.InvalidField(path, "%s: %s", path,
					strings.TrimPrefix(err.Error(), domain.ErrInvalidArgument.Error()+": "))
			}
		}
		if err := errors.Join(checkBinding(at+".consolidate_model", s.ConsolidateModel),
			checkBinding(at+".dedup_check_model", s.DedupCheckModel)); err != nil {
			return err
		}
	}
	return nil
}
