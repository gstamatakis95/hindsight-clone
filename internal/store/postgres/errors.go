package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres SQLSTATE codes the adapter maps to domain errors.
const (
	codeUniqueViolation      = "23505"
	codeForeignKeyViolation  = "23503"
	codeCheckViolation       = "23514"
	codeNotNullViolation     = "23502"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
	codeLockNotAvailable     = "55P03"
	codeProgramLimitExceeded = "54000"
	// classDataException is the SQLSTATE class of data exceptions (22xxx).
	classDataException = "22"
)

// mapErr translates a driver error into a domain error class, wrapping the original with %w so both
// errors.Is(err, domain.ErrX) and the driver detail survive. Domain errors, context errors and nil pass
// through unchanged. what names the failed operation for the message.
func mapErr(what string, err error) error {
	if err == nil || isDomainErr(err) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", what, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%w: %s: %w", pgClass(pgErr.Code), what, err)
	}
	// Connection loss, timeouts and any other driver failure.
	return fmt.Errorf("%w: %s: %w", domain.ErrUnavailable, what, err)
}

// pgClass picks the domain error class of a Postgres SQLSTATE.
func pgClass(code string) error {
	switch code {
	case codeUniqueViolation:
		return domain.ErrAlreadyExists
	case codeForeignKeyViolation:
		return domain.ErrNotFound
	case codeCheckViolation, codeNotNullViolation, codeProgramLimitExceeded:
		return domain.ErrInvalidArgument
	case codeSerializationFailure, codeDeadlockDetected, codeLockNotAvailable:
		return domain.ErrConflict
	}
	if strings.HasPrefix(code, classDataException) {
		// Bad input (NUL bytes, invalid encodings, out-of-range values; 22P02 included): deterministic, so
		// the job fails at once instead of retrying (SPEC "Jobs").
		return domain.ErrInvalidArgument
	}
	return domain.ErrUnavailable
}

// isDomainErr reports whether err already carries a domain error class.
func isDomainErr(err error) bool {
	for _, target := range []error{
		domain.ErrNotFound, domain.ErrAlreadyExists, domain.ErrInvalidArgument, domain.ErrFailedPrecondition,
		domain.ErrUnavailable, domain.ErrResourceExhausted, domain.ErrCancelled, domain.ErrConflict,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// isPgCode reports whether err is (or wraps) a Postgres error with the given SQLSTATE.
func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
