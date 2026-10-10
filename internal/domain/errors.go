package domain

import (
	"errors"
	"fmt"
)

// Error values shared by every layer. Wrap them with fmt.Errorf("%w: ...", ErrX) to add context;
// callers test with errors.Is. The server maps each class to one gRPC code.
var (
	// ErrNotFound: the bank, document, unit, model, directive or operation does not exist
	// (also any read of a deleting bank). gRPC NOT_FOUND.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists: a create collided with an existing id. gRPC ALREADY_EXISTS.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalidArgument: the request is malformed or uses a field the RPC can't use. gRPC INVALID_ARGUMENT.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrFailedPrecondition: the state forbids the call (wrong operation status, ...). gRPC FAILED_PRECONDITION.
	ErrFailedPrecondition = errors.New("failed precondition")
	// ErrBankDeleting: a write to a tombstoned bank. Wraps ErrFailedPrecondition.
	ErrBankDeleting = fmt.Errorf("%w: bank is being deleted", ErrFailedPrecondition)
	// ErrUnavailable: a dependency (database, gateway, embedder, reranker) failed. gRPC UNAVAILABLE.
	ErrUnavailable = errors.New("unavailable")
	// ErrResourceExhausted: a token budget such as max_total_tokens was exceeded. gRPC RESOURCE_EXHAUSTED.
	ErrResourceExhausted = errors.New("resource exhausted")
	// ErrTooManyScopes: ALL_COMBINATIONS fan-out over max_combinations. Wraps ErrInvalidArgument.
	ErrTooManyScopes = fmt.Errorf("%w: too many observation scopes", ErrInvalidArgument)
	// ErrCancelled: the operation was cancelled or its bank deleted; handlers stop at the checkpoint.
	ErrCancelled = errors.New("operation cancelled")
	// ErrConflict: a concurrent writer won a race (e.g. a serial-key claim); the caller may retry.
	ErrConflict = errors.New("conflict")
)
