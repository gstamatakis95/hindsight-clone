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
