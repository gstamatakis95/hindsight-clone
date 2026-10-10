package consolidate

import (
	"memd/internal/domain"
	"memd/internal/port"
)

// ScopeExpander turns a unit's tags and scope mode into the scopes it consolidates under.
type ScopeExpander interface {
	// Expand returns the scopes for tags under s. ALL_COMBINATIONS beyond max_combinations returns
	// ErrTooManyScopes; the Ingestor calls Expand before storing anything, so Retain fails with
	// INVALID_ARGUMENT instead of a job failing later.
	Expand(b port.BankRef, tags []string, s domain.ObservationScopes) ([]port.ScopeKey, error)
}
