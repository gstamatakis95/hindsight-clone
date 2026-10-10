package server

import (
	"memd/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// notWired is the error of an RPC whose dependency cmd/memd/wire.go left nil.
func notWired(dep string) error {
	return status.Errorf(codes.Unimplemented, "%s is not wired", dep)
}

// validate runs Deps.ValidateOverrides for rpc on request overrides o; nil o or no validator passes.
func (d Deps) validate(rpc string, o *domain.BankConfig) error {
	if o == nil || d.ValidateOverrides == nil {
		return nil
	}
	return d.ValidateOverrides(rpc, *o)
}

// validatedSpec wraps patch so the patched spec's overrides pass the same check as CreateMentalModel. It
// stays pure: the implementation runs it inside its transaction.
func (d Deps) validatedSpec(patch SpecPatch) SpecPatch {
	return func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		out, err := patch(cur)
		if err != nil {
			return domain.MentalModelSpec{}, err
		}
		if err := d.validate("CreateMentalModel", out.Overrides); err != nil {
			return domain.MentalModelSpec{}, err
		}
		return out, nil
	}
}

