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
