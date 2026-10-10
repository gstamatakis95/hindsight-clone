package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"memd/internal/domain"
)

// errorCodes maps each domain error class to its gRPC code, checked in order with errors.Is. Wrapping
// sentinels (ErrBankDeleting wraps ErrFailedPrecondition, ErrTooManyScopes wraps ErrInvalidArgument)
// land on their parent's code.
var errorCodes = []struct {
	err  error
	code codes.Code
}{
	{context.DeadlineExceeded, codes.DeadlineExceeded},
	{context.Canceled, codes.Canceled},
	{domain.ErrNotFound, codes.NotFound},
	{domain.ErrAlreadyExists, codes.AlreadyExists},
	{domain.ErrInvalidArgument, codes.InvalidArgument},
	{domain.ErrFailedPrecondition, codes.FailedPrecondition},
	{domain.ErrUnavailable, codes.Unavailable},
	{domain.ErrResourceExhausted, codes.ResourceExhausted},
	{domain.ErrCancelled, codes.Aborted},
	{domain.ErrConflict, codes.Aborted},
}

// toStatus maps a domain error to a gRPC status error (decision 602). nil stays nil; an error that already
// carries a gRPC status passes through unchanged; anything unclassified is INTERNAL.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	for _, m := range errorCodes {
		if errors.Is(err, m.err) {
			return status.Error(m.code, err.Error())
		}
	}
	return status.Error(codes.Internal, err.Error())
}
