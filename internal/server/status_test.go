package server

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestToStatus checks the error mapping table, wrapped errors and the two wrapping sentinels.
func TestToStatus(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("engine: %w", err) }
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", domain.ErrNotFound, codes.NotFound},
		{"wrapped not found", wrap(domain.ErrNotFound), codes.NotFound},
		{"already exists", domain.ErrAlreadyExists, codes.AlreadyExists},
		{"invalid argument", wrap(domain.ErrInvalidArgument), codes.InvalidArgument},
		{"too many scopes", wrap(domain.ErrTooManyScopes), codes.InvalidArgument},
		{"failed precondition", domain.ErrFailedPrecondition, codes.FailedPrecondition},
		{"deleting bank", wrap(domain.ErrBankDeleting), codes.FailedPrecondition},
		{"unavailable", wrap(domain.ErrUnavailable), codes.Unavailable},
		{"resource exhausted", wrap(domain.ErrResourceExhausted), codes.ResourceExhausted},
		{"operation cancelled", domain.ErrCancelled, codes.Aborted},
		{"conflict", domain.ErrConflict, codes.Aborted},
		{"lost jobs", &port.LostJobsError{IDs: []port.OperationID{"x"}}, codes.Aborted},
		{"context cancelled", wrap(context.Canceled), codes.Canceled},
		{"deadline", wrap(context.DeadlineExceeded), codes.DeadlineExceeded},
		{
			"deadline wins over unavailable", fmt.Errorf("%w: %w", domain.ErrUnavailable, context.DeadlineExceeded),
			codes.DeadlineExceeded,
		},
		{"unclassified", errors.New("boom"), codes.Internal},
		{"status passes through", status.Error(codes.PermissionDenied, "no"), codes.PermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := status.Code(toStatus(tt.err))
			if got != tt.want {
				t.Fatalf("toStatus(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
	if toStatus(nil) != nil {
		t.Fatal("toStatus(nil) != nil")
	}
	if msg := status.Convert(toStatus(wrap(domain.ErrNotFound))).Message(); msg != "engine: not found" {
		t.Fatalf("message = %q, want the error text", msg)
	}
}
