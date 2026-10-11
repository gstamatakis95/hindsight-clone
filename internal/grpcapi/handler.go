package grpcapi

import (
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/mentalmodel"
)

// validate checks a request's or a stored spec's overrides with config.Validate; nil passes.
func validate(o *domain.BankConfig) error {
	if o == nil {
		return nil
	}
	return config.Validate(*o) //nolint:wrapcheck // config errors are already domain error classes
}

// validatedSpec wraps patch so the patched spec's overrides pass the same check as CreateMentalModel. It
// stays pure: the implementation runs it inside its transaction.
func validatedSpec(patch mentalmodel.SpecPatch) mentalmodel.SpecPatch {
	return func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		out, err := patch(cur)
		if err != nil {
			return domain.MentalModelSpec{}, err
		}
		if err := validate(out.Overrides); err != nil {
			return domain.MentalModelSpec{}, err
		}
		return out, nil
	}
}
