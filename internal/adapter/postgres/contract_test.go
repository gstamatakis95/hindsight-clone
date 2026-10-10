package postgres_test

import (
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/port"
	"memd/internal/porttest"
	"os"
	"testing"
)

// TestMain starts one migrated ParadeDB container for the package's tests.
func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// newStore returns a Store over the freshly truncated shared database.
func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	return postgres.New(pgtest.Fresh(t))
}

// storeFactory adapts newStore to porttest.StoreFactory.
func storeFactory(t *testing.T) port.Store { return newStore(t) }

// queueFactory returns the Store and its Claimer (the same value) over the truncated database.
func queueFactory(t *testing.T) (port.Store, port.Claimer) {
	s := newStore(t)
	return s, s
}

// TestStoreContract runs porttest.RunStoreSuite against Postgres.
func TestStoreContract(t *testing.T) { porttest.RunStoreSuite(t, storeFactory) }

// TestSeqContract runs porttest.RunSeqSuite against Postgres.
func TestSeqContract(t *testing.T) { porttest.RunSeqSuite(t, storeFactory) }

// TestQueueContract runs porttest.RunQueueSuite against Postgres.
func TestQueueContract(t *testing.T) { porttest.RunQueueSuite(t, queueFactory) }

// TestReadsContract runs porttest.RunReadsSuite (WP-15) against Postgres.
func TestReadsContract(t *testing.T) { porttest.RunReadsSuite(t, storeFactory) }

// TestCurationReadsContract runs porttest.RunCurationReadsSuite (WP-15) against Postgres.
func TestCurationReadsContract(t *testing.T) { porttest.RunCurationReadsSuite(t, storeFactory) }

// TestMaintenanceContract runs porttest.RunMaintenanceSuite (WP-15) against Postgres.
func TestMaintenanceContract(t *testing.T) { porttest.RunMaintenanceSuite(t, queueFactory) }

// TestObservationAdminContract runs porttest.RunObservationAdminSuite (WP-15) against Postgres.
func TestObservationAdminContract(t *testing.T) { porttest.RunObservationAdminSuite(t, storeFactory) }
