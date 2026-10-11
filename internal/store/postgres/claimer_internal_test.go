package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestLostClaimRace checks which errors make Claim retry: a unique violation of the processing serial-key
// index or a deadlock, also when wrapped; never another constraint or a non-Postgres error.
func TestLostClaimRace(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"processing index", &pgconn.PgError{Code: codeUniqueViolation, ConstraintName: processingKeyIndex}, true},
		{"wrapped", fmt.Errorf("claim: %w", &pgconn.PgError{
			Code: codeUniqueViolation, ConstraintName: processingKeyIndex,
		}), true},
		{"deadlock", &pgconn.PgError{Code: codeDeadlockDetected}, true},
		{"other unique index", &pgconn.PgError{Code: codeUniqueViolation, ConstraintName: "operations_pkey"}, false},
		{"check violation", &pgconn.PgError{Code: codeCheckViolation}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := lostClaimRace(c.err); got != c.want {
			t.Errorf("%s: lostClaimRace = %v, want %v", c.name, got, c.want)
		}
	}
}
