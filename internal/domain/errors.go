package domain

import (
	"errors"
	"fmt"
	"time"
)

// Error is a domain error class: a sentinel with a stable machine-readable reason (UPPER_SNAKE), which the
// gRPC layer sends as google.rpc.ErrorInfo.reason. A class may refine a parent class: errors.Is matches both,
// so code that tests the parent keeps working, and [ReasonOf] picks the most specific reason. Wrap a class
// with fmt.Errorf("%w: ...", ErrX) to add context.
type Error struct {
	// reason is the stable reason code.
	reason string
	// msg is the class's own message.
	msg string
	// parent is the refined class; nil for a root class.
	parent *Error
}

// Error returns the message, prefixed by the parent class's message.
func (e *Error) Error() string {
	if e.parent == nil {
		return e.msg
	}
	return e.parent.Error() + ": " + e.msg
}

// Unwrap returns the parent class, so errors.Is matches it; nil for a root class.
func (e *Error) Unwrap() error {
	if e.parent == nil {
		return nil
	}
	return e.parent
}

// Reason returns the stable reason code.
func (e *Error) Reason() string { return e.reason }

// newClass returns a root error class.
func newClass(reason, msg string) *Error { return &Error{reason: reason, msg: msg} }

// refine returns a class that refines parent.
func refine(parent *Error, reason, msg string) *Error {
	return &Error{reason: reason, msg: msg, parent: parent}
}

// Error values shared by every layer. Wrap them with fmt.Errorf("%w: ...", ErrX) to add context; callers test
// with errors.Is. The server maps each root class to one gRPC code and sends the most specific reason.
var (
	// ErrNotFound: the org, bank, document, unit, model, directive or operation does not exist (also any read of a
	// deleting bank). gRPC NOT_FOUND.
	ErrNotFound = newClass("NOT_FOUND", "not found")
	// ErrBankNotFound: the bank does not exist, or a read hit a bank being deleted. Refines ErrNotFound.
	ErrBankNotFound = refine(ErrNotFound, "BANK_NOT_FOUND", "bank")
	// ErrOrgNotFound: the org does not exist, or it is being deleted. Orgs are never created on first use, so a
	// write under an unknown org fails with it too. Refines ErrNotFound.
	ErrOrgNotFound = refine(ErrNotFound, "ORG_NOT_FOUND", "org")
	// ErrAlreadyExists: a create collided with an existing id. gRPC ALREADY_EXISTS.
	ErrAlreadyExists = newClass("ALREADY_EXISTS", "already exists")
	// ErrInvalidArgument: the request is malformed or uses a field the RPC can't use. gRPC INVALID_ARGUMENT.
	// [InvalidField] builds one that names the offending field.
	ErrInvalidArgument = newClass("INVALID_ARGUMENT", "invalid argument")
	// ErrTooManyScopes: ALL_COMBINATIONS fan-out over max_combinations. Refines ErrInvalidArgument.
	ErrTooManyScopes = refine(ErrInvalidArgument, "TOO_MANY_SCOPES", "too many observation scopes")
	// ErrMemoryIsObservation: a curation call named an observation; observations re-derive from their
	// sources. Refines ErrInvalidArgument.
	ErrMemoryIsObservation = refine(ErrInvalidArgument, "MEMORY_IS_OBSERVATION",
		"observations are not curated directly; they re-derive from their sources")
	// ErrFailedPrecondition: the state forbids the call (wrong operation status, ...). gRPC FAILED_PRECONDITION.
	ErrFailedPrecondition = newClass("FAILED_PRECONDITION", "failed precondition")
	// ErrBankDeleting: a write to a tombstoned bank. Refines ErrFailedPrecondition.
	ErrBankDeleting = refine(ErrFailedPrecondition, "BANK_DELETING", "bank is being deleted")
	// ErrOrgDeleting: a write under an org that is being deleted. Refines ErrFailedPrecondition.
	ErrOrgDeleting = refine(ErrFailedPrecondition, "ORG_DELETING", "org is being deleted")
	// ErrModelUnbound: an LLM slot has no model bound in the effective config (the deployment or the tenant or bank
	// config lacks a binding; the request itself is fine). Refines ErrFailedPrecondition; a job that hits it fails at
	// once instead of retrying.
	ErrModelUnbound = refine(ErrFailedPrecondition, "MODEL_UNBOUND", "model slot unbound")
	// ErrOperationNotCancellable: CancelOperation on a terminal operation or a delete_bank. Refines
	// ErrFailedPrecondition.
	ErrOperationNotCancellable = refine(ErrFailedPrecondition, "OPERATION_NOT_CANCELLABLE",
		"operation can't be cancelled")
	// ErrOperationNotRetryable: RetryOperation on an operation that is not FAILED or CANCELLED. Refines
	// ErrFailedPrecondition.
	ErrOperationNotRetryable = refine(ErrFailedPrecondition, "OPERATION_NOT_RETRYABLE", "operation can't be retried")
	// ErrUnavailable: a dependency (database, gateway, embedder, reranker) failed. gRPC UNAVAILABLE.
	ErrUnavailable = newClass("UNAVAILABLE", "unavailable")
	// ErrContextOverflow: the prompt did not fit the model's context window. Refines ErrUnavailable; reflect
	// synthesises from what it has instead of failing.
	ErrContextOverflow = refine(ErrUnavailable, "CONTEXT_OVERFLOW", "context window exceeded")
	// ErrRetrievalUnavailable: a reflect retrieval tool failed, so reflect (strict) fails instead of answering
	// from partial evidence. Refines ErrUnavailable.
	ErrRetrievalUnavailable = refine(ErrUnavailable, "RETRIEVAL_UNAVAILABLE", "retrieval failed")
	// ErrResourceExhausted: a token or size budget was exceeded. gRPC RESOURCE_EXHAUSTED.
	ErrResourceExhausted = newClass("RESOURCE_EXHAUSTED", "resource exhausted")
	// ErrMaxTotalTokens: a reflect call went over max_total_tokens. Refines ErrResourceExhausted.
	ErrMaxTotalTokens = refine(ErrResourceExhausted, "MAX_TOTAL_TOKENS_EXCEEDED", "max_total_tokens exceeded")
	// ErrIngestBacklog: RetainBatch was rejected whole, before anything was written, because the bank's or the
	// org's pending retain backlog is over its limit; retry after the delay [RetryDelay] reports. Refines
	// ErrResourceExhausted.
	ErrIngestBacklog = refine(ErrResourceExhausted, "INGEST_BACKLOG", "retain backlog over its limit")
	// ErrResponseTooLarge: a read's response would pass the server's response size limit
	// (api.max_response_bytes). Refines ErrResourceExhausted.
	ErrResponseTooLarge = refine(ErrResourceExhausted, "RESPONSE_TOO_LARGE", "response too large")
	// ErrCancelled: the operation was cancelled or its bank deleted; handlers stop at the checkpoint.
	ErrCancelled = newClass("OPERATION_CANCELLED", "operation cancelled")
	// ErrConflict: a concurrent writer won a race (e.g. a serial-key claim); the caller may retry.
	ErrConflict = newClass("CONFLICT", "conflict")
)

// ReasonOf returns the reason of the most specific domain error class in err's tree (the first one errors.As
// finds), or "" when err carries none.
func ReasonOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.reason
	}
	return ""
}

// FieldViolation names one invalid request field, as a google.rpc.BadRequest field violation.
type FieldViolation struct {
	// Field is the path of the field in the request, for example page_size or tags.groups; for a config, the
	// BankConfig path, for example reflect.disposition.empathy.
	Field string
	// Description says what is wrong with it.
	Description string
}

// fieldError is an ErrInvalidArgument that names the offending field.
type fieldError struct {
	// v is the violation.
	v FieldViolation
}

// Error returns "invalid argument: " followed by the description.
func (e *fieldError) Error() string { return ErrInvalidArgument.Error() + ": " + e.v.Description }

// Unwrap returns ErrInvalidArgument.
func (e *fieldError) Unwrap() error { return ErrInvalidArgument }

// InvalidField returns an ErrInvalidArgument error naming the request field at path field; the formatted
// description is also the error's message (after "invalid argument: ").
func InvalidField(field, format string, args ...any) error {
	return &fieldError{v: FieldViolation{Field: field, Description: fmt.Sprintf(format, args...)}}
}

// FieldViolations returns every field violation in err's tree, depth first (errors.Join and multi-%w
// included); nil when there is none.
func FieldViolations(err error) []FieldViolation {
	var out []FieldViolation
	var walk func(error)
	walk = func(e error) {
		switch x := e.(type) { //nolint:errorlint // walks every node of the tree itself, wrapped ones included
		case nil:
			return
		case *fieldError:
			out = append(out, x.v)
			return
		case interface{ Unwrap() []error }:
			for _, c := range x.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(x.Unwrap())
		}
	}
	walk(err)
	return out
}

// retryError carries the delay after which a client should retry; its Unwrap is the wrapped error.
type retryError struct {
	// err is the wrapped error.
	err error
	// after is the retry delay.
	after time.Duration
}

// Error returns the wrapped error's message.
func (e *retryError) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error.
func (e *retryError) Unwrap() error { return e.err }

// WithRetryDelay returns err carrying a retry delay, which the API sends as google.rpc.RetryInfo; errors.Is and
// [ReasonOf] still see err's classes.
func WithRetryDelay(err error, after time.Duration) error { return &retryError{err: err, after: after} }

// RetryDelay returns the retry delay err carries ([WithRetryDelay]), and false when it carries none.
func RetryDelay(err error) (time.Duration, bool) {
	var r *retryError
	if errors.As(err, &r) {
		return r.after, true
	}
	return 0, false
}
