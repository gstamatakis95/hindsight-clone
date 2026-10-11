package config

import (
	"errors"
	"fmt"
	"memd/internal/domain"
)

// Validate checks one config level below the server: a tenant or bank config, a bank template, a request's
// overrides or a stored spec's overrides. On top of ValidateServer's rules it rejects models.embed, because
// every stored embedding shares one vector space, so the embedding model is chosen once, in the server config
// file. Failures are domain.ErrInvalidArgument.
func Validate(c domain.BankConfig) error {
	if c.Models != nil && c.Models.Embed != nil {
		return fmt.Errorf("%w: models.embed can only be set in the server config file", domain.ErrInvalidArgument)
	}
	return ValidateServer(c)
}

// ValidateServer checks a config level, the server level included: each disposition trait in 0..5 (0 = unset),
// consolidation strategy names unique, a reconcile grace that is not negative, and every set model binding
// naming a model. Failures are domain.ErrInvalidArgument.
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
			errs = append(errs, fmt.Errorf("observations.reconcile_grace_seconds = %d, want >= 0", *g))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrInvalidArgument, err)
	}
	return nil
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
			return fmt.Errorf("reflect.disposition.%s = %d, want 1-5 (0 = unset)", t.name, t.v)
		}
	}
	return nil
}

// checkBinding requires a set binding to name a model.
func checkBinding(path string, b *domain.ModelBinding) error {
	if b != nil && b.Model == "" {
		return fmt.Errorf("%s: model is required", path)
	}
	return nil
}

// checkStrategies requires strategy names to be unique and each strategy's bindings to name a model.
func checkStrategies(strategies []domain.ConsolidationStrategy) error {
	seen := map[string]bool{}
	for i, s := range strategies {
		at := fmt.Sprintf("observations.strategies[%d]", i)
		if s.Name != "" && seen[s.Name] {
			return fmt.Errorf("%s: name %q is used twice", at, s.Name)
		}
		seen[s.Name] = true
		if err := errors.Join(checkBinding(at+".consolidate_model", s.ConsolidateModel),
			checkBinding(at+".dedup_check_model", s.DedupCheckModel)); err != nil {
			return err
		}
	}
	return nil
}
