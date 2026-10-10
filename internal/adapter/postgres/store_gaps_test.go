package postgres_test

import (
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/port"
	"memd/internal/porttest"
	"testing"
)

// TestStoreGapsContract runs porttest.RunStoreGapsSuite against Postgres.
func TestStoreGapsContract(t *testing.T) {
	porttest.RunStoreGapsSuite(t, func(t *testing.T) port.Store { return postgres.New(pgtest.Fresh(t)) })
}
