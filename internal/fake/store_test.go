package fake_test

import (
	"memd/internal/fake"
	"memd/internal/port"
	"memd/internal/porttest"
	"testing"
	"time"
)

// wallClock is the real clock the contract suites need: they compare run_at with "now ± 1 h" and sleep
// past heartbeat ttls.
type wallClock struct{}

// Now returns the wall-clock time in UTC.
func (wallClock) Now() time.Time { return time.Now().UTC() }

// newWallStore returns an empty store on the wall clock.
func newWallStore(*testing.T) port.Store { return fake.NewStore(wallClock{}) }

func TestStoreContract(t *testing.T) {
	porttest.RunStoreSuite(t, newWallStore)
}

func TestSeqContract(t *testing.T) {
	porttest.RunSeqSuite(t, newWallStore)
}

func TestQueueContract(t *testing.T) {
	porttest.RunQueueSuite(t, func(*testing.T) (port.Store, port.Claimer) {
		s := fake.NewStore(wallClock{})
		return s, s
	})
}

// TestStoreGapsContract runs the store gap cases added in WP-01 (tenant isolation of the round-1/2 write ports).
func TestStoreGapsContract(t *testing.T) {
	porttest.RunStoreGapsSuite(t, newWallStore)
}

// TestQueueGapsContract runs the queue gap cases added in WP-01 (cancel-requested Fail, tombstoned banks,
// join-and-upgrade versus a concurrent claim).
func TestQueueGapsContract(t *testing.T) {
	porttest.RunQueueGapsSuite(t, func(*testing.T) (port.Store, port.Claimer) {
		s := fake.NewStore(wallClock{})
		return s, s
	})
}
