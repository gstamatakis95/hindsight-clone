package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestToStatus checks the error mapping: the code of every domain class (refined classes land on their
// parent's code), and the ErrorInfo every status carries, with domain memd and the most specific reason.
func TestToStatus(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("engine: %w", err) }
	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{"not found", domain.ErrNotFound, codes.NotFound, "NOT_FOUND"},
		{"bank not found", wrap(domain.ErrBankNotFound), codes.NotFound, "BANK_NOT_FOUND"},
		{"already exists", domain.ErrAlreadyExists, codes.AlreadyExists, "ALREADY_EXISTS"},
		{"invalid argument", wrap(domain.ErrInvalidArgument), codes.InvalidArgument, "INVALID_ARGUMENT"},
		{"too many scopes", wrap(domain.ErrTooManyScopes), codes.InvalidArgument, "TOO_MANY_SCOPES"},
		{"observation", wrap(domain.ErrMemoryIsObservation), codes.InvalidArgument, "MEMORY_IS_OBSERVATION"},
		{"failed precondition", domain.ErrFailedPrecondition, codes.FailedPrecondition, "FAILED_PRECONDITION"},
		{"deleting bank", wrap(domain.ErrBankDeleting), codes.FailedPrecondition, "BANK_DELETING"},
		{"model unbound", wrap(domain.ErrModelUnbound), codes.FailedPrecondition, "MODEL_UNBOUND"},
		{
			"not cancellable", wrap(domain.ErrOperationNotCancellable), codes.FailedPrecondition,
			"OPERATION_NOT_CANCELLABLE",
		},
		{"not retryable", wrap(domain.ErrOperationNotRetryable), codes.FailedPrecondition, "OPERATION_NOT_RETRYABLE"},
		{"unavailable", wrap(domain.ErrUnavailable), codes.Unavailable, "UNAVAILABLE"},
		{"retrieval", wrap(domain.ErrRetrievalUnavailable), codes.Unavailable, "RETRIEVAL_UNAVAILABLE"},
		{"resource exhausted", wrap(domain.ErrResourceExhausted), codes.ResourceExhausted, "RESOURCE_EXHAUSTED"},
		{"max total tokens", wrap(domain.ErrMaxTotalTokens), codes.ResourceExhausted, "MAX_TOTAL_TOKENS_EXCEEDED"},
		{"operation cancelled", domain.ErrCancelled, codes.Aborted, "OPERATION_CANCELLED"},
		{"conflict", domain.ErrConflict, codes.Aborted, "CONFLICT"},
		{"lost jobs", &store.LostJobsError{IDs: []domain.OperationID{"x"}}, codes.Aborted, "CONFLICT"},
		{"context cancelled", wrap(context.Canceled), codes.Canceled, "CANCELLED"},
		{"deadline", wrap(context.DeadlineExceeded), codes.DeadlineExceeded, "DEADLINE_EXCEEDED"},
		{
			"deadline wins over unavailable", fmt.Errorf("%w: %w", domain.ErrUnavailable, context.DeadlineExceeded),
			codes.DeadlineExceeded, "DEADLINE_EXCEEDED",
		},
		{
			"cancel wins over a domain error", fmt.Errorf("%w: %w", domain.ErrNotFound, context.Canceled),
			codes.Canceled, "CANCELLED",
		},
		{"unclassified", errors.New("boom"), codes.Internal, "INTERNAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := status.Convert(toStatus(tt.err))
			if st.Code() != tt.code {
				t.Fatalf("toStatus(%v) = %v, want %v", tt.err, st.Code(), tt.code)
			}
			info := errorInfo(t, st)
			if info.GetReason() != tt.reason || info.GetDomain() != ErrorDomain {
				t.Fatalf("ErrorInfo = %v, want reason %s in domain %s", info, tt.reason, ErrorDomain)
			}
		})
	}
	if toStatus(nil) != nil {
		t.Fatal("toStatus(nil) != nil")
	}
	passed := status.Error(codes.PermissionDenied, "no")
	if got := status.Code(toStatus(fmt.Errorf("x: %w", passed))); got != codes.PermissionDenied {
		t.Fatalf("a wrapped status = %v, want it passed through", got)
	}
	if msg := status.Convert(toStatus(wrap(domain.ErrNotFound))).Message(); msg != "engine: not found" {
		t.Fatalf("message = %q, want the error text", msg)
	}
}

// TestToStatusFieldViolations checks an INVALID_ARGUMENT that names request fields carries them as a BadRequest:
// a request field, and every path of a joined config validation error.
func TestToStatusFieldViolations(t *testing.T) {
	err := toStatus(fmt.Errorf("handler: %w", domain.InvalidField("page_size", "page_size -1 must not be negative")))
	if got := fieldsOf(err); !slices.Equal(got, []string{"page_size"}) {
		t.Fatalf("field violations = %v, want [page_size]", got)
	}
	bad := domain.BankConfig{
		Reflect: &domain.ReflectConfig{Disposition: &domain.Disposition{Empathy: 9}},
		Models:  &domain.ModelBindings{Rerank: &domain.ModelBinding{}},
	}
	err = toStatus(config.Validate(bad))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("config error = %v, want INVALID_ARGUMENT", err)
	}
	want := []string{"reflect.disposition.empathy", "models.rerank.model"}
	if got := fieldsOf(err); !slices.Equal(got, want) {
		t.Fatalf("field violations = %v, want %v", got, want)
	}
	if got := fieldsOf(toStatus(domain.ErrNotFound)); got != nil {
		t.Fatalf("NOT_FOUND carries field violations %v", got)
	}
}

// errorInfo returns st's ErrorInfo detail, failing when there is none.
func errorInfo(t *testing.T, st *status.Status) *errdetails.ErrorInfo {
	t.Helper()
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatalf("status %v has no ErrorInfo", st)
	return nil
}

// fieldsOf returns the field paths of err's BadRequest detail; nil when it has none.
func fieldsOf(err error) []string {
	var out []string
	for _, d := range status.Convert(err).Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, v := range br.GetFieldViolations() {
				out = append(out, v.GetField())
			}
		}
	}
	return out
}
