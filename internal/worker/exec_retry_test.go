package worker

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// alwaysPending is a PendingChecker reporting an automatic consolidate due for every bank.
type alwaysPending struct{ calls int }

func (a *alwaysPending) HasPending(context.Context, port.BankRef, port.JobKind) (bool, error) {
	a.calls++
	return true, nil
}

// Accept (WP-20 item 3): a failing manual consolidate (scopes or overrides) keeps its retry even when an
// automatic consolidate is due; only an automatic one skips its retry.
func TestManualConsolidateRetriedDespitePending(t *testing.T) {
	boom := errors.New("gateway timeout")
	tests := []struct {
		name      string
		payload   string
		overrides *domain.BankConfig
		retry     bool
	}{
		{"automatic empty payload", `{}`, nil, false},
		{"automatic no payload", ``, nil, false},
		{"automatic empty scopes", `{"scopes":[]}`, nil, false},
		{"manual scopes", `{"scopes":[{"tags":["a"]}]}`, nil, true},
		{"manual overrides", `{}`, &domain.BankConfig{AutoConsolidation: ptrBool(true)}, true},
		{"undecodable payload", `{"scopes":`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			pending := &alwaysPending{}
			r := testRunner(newOps(clock), clock, RunnerConfig{Pending: pending})
			j := consolidateJob(bankA)
			j.Payload, j.Overrides = json.RawMessage(tc.payload), tc.overrides
			at := r.retryAt(context.Background(), j, boom)
			if got := at != nil; got != tc.retry {
				t.Fatalf("retry = %v (at %v), want %v", got, at, tc.retry)
			}
			if !tc.retry && pending.calls != 1 {
				t.Errorf("pending probe calls = %d, want 1", pending.calls)
			}
		})
	}
}
