package grpcapi

import (
	"context"
	"errors"
	"memd/internal/domain"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
)

// ErrorDomain is the google.rpc.ErrorInfo domain of every memd error.
const ErrorDomain = "memd"

// errorCodes maps each domain error class to its gRPC code, checked in order with errors.Is. Refined classes
// (domain.ErrBankDeleting refines ErrFailedPrecondition, ErrTooManyScopes refines ErrInvalidArgument, ...)
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

// codeReasons are the ErrorInfo reasons of errors that carry no domain class: the context errors and the
// unclassified INTERNAL.
var codeReasons = map[codes.Code]string{
	codes.DeadlineExceeded: "DEADLINE_EXCEEDED",
	codes.Canceled:         "CANCELLED",
	codes.Internal:         "INTERNAL",
}

// toStatus maps a domain error to a gRPC status error. nil stays nil; an error that already carries a gRPC
// status passes through unchanged; anything unclassified is INTERNAL. Every status carries a
// google.rpc.ErrorInfo (domain "memd", reason = the most specific domain class's reason, domain.ReasonOf), and
// one whose error names request fields (domain.FieldViolations) also a google.rpc.BadRequest.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	code := codes.Internal
	for _, m := range errorCodes {
		if errors.Is(err, m.err) {
			code = m.code
			break
		}
	}
	return withDetails(status.New(code, err.Error()), err).Err() //nolint:wrapcheck // the status goes to the client as is
}

// withDetails attaches err's ErrorInfo and, when it names fields, its BadRequest to st. A detail that fails to
// marshal (never for these message types) leaves st without details.
func withDetails(st *status.Status, err error) *status.Status {
	reason := domain.ReasonOf(err)
	if reason == "" || st.Code() == codes.DeadlineExceeded || st.Code() == codes.Canceled {
		reason = codeReasons[st.Code()]
	}
	details := []protoadapt.MessageV1{&errdetails.ErrorInfo{Reason: reason, Domain: ErrorDomain}}
	if vs := domain.FieldViolations(err); len(vs) > 0 {
		br := &errdetails.BadRequest{FieldViolations: make([]*errdetails.BadRequest_FieldViolation, len(vs))}
		for i, v := range vs {
			br.FieldViolations[i] = &errdetails.BadRequest_FieldViolation{Field: v.Field, Description: v.Description}
		}
		details = append(details, br)
	}
	out, derr := st.WithDetails(details...)
	if derr != nil {
		return st
	}
	return out
}
