package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
	"time"
)

// alwaysPending is a pendingChecker reporting an automatic consolidate due for every bank.
type alwaysPending struct{ calls int }

func (a *alwaysPending) hasPending(context.Context, domain.BankRef) (bool, error) {
	a.calls++
	return true, nil
}

// ptrBool returns &v.
func ptrBool(v bool) *bool { return &v }

// A failing manual consolidate (scopes or overrides) keeps its retry even when an
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
		{"manual overrides", `{}`, &domain.BankConfig{
			Observations: &domain.ObservationConfig{AutoConsolidation: ptrBool(true)},
		}, true},
		{"undecodable payload", `{"scopes":`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			pending := &alwaysPending{}
			r := testRunner(newOps(t), clock, Config{})
			r.pending = pending
			j := consolidateRow(bankA)
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

func TestPendingChecker(t *testing.T) {
	bg := context.Background()
	clock := newClock()
	ops := newOps(t)
	pc := &consolidateProbe{s: ops, c: clock}
	due := consolidateRow(bankA)
	due.RunAt = clock.Now() // due-ness is judged by the checker's clock, not the database's insert time
	parked := consolidateRow(bankB)
	parked.RunAt = clock.Now().Add(time.Hour)
	ops.add(due)
	ops.add(parked)
	ops.add(store.Job{
		Bank: bankC, Kind: domain.OperationKindConsolidate, SerialKey: SerialKeyConsolidate,
		Overrides: &domain.BankConfig{},
	}) // manual, with overrides: not joinable
	ops.markDeleting(domain.NewBankRef("t9", "org", "gone"))
	rows := ops.count("true")
	tests := []struct {
		bank domain.BankRef
		want bool
	}{
		{bankA, true},  // due automatic consolidate
		{bankB, false}, // parked
		{bankC, false}, // not joinable
		{domain.NewBankRef("t9", "org", "gone"), false}, // tombstoned
	}
	for _, tt := range tests {
		if ok, err := pc.hasPending(bg, tt.bank); err != nil || ok != tt.want {
			t.Errorf("bank %v = %v, %v; want %v", tt.bank, ok, err, tt.want)
		}
	}
	if _, err := pc.hasPending(bg, domain.NewBankRef("t9", "org", "x")); !errors.Is(
		err, domain.ErrNotFound) {
		t.Errorf("unknown bank = %v, want ErrNotFound", err)
	}
	if n := ops.count("true"); n != rows {
		t.Errorf("probe left %d rows, want %d (always rolled back)", n, rows)
	}
	clock.Advance(2 * time.Hour)
	if ok, _ := pc.hasPending(bg, bankB); !ok {
		t.Error("a parked consolidate that became due must count")
	}
	broken := &consolidateProbe{s: failingDB{DB: ops, err: domain.ErrUnavailable}, c: clock}
	if _, err := broken.hasPending(bg, bankA); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("broken store = %v, want ErrUnavailable", err)
	}
}

// failingDB is a store.DB whose every transaction fails with err.
type failingDB struct {
	store.DB
	err error
}

func (f failingDB) InTx(context.Context, func(store.Tx) error) error { return f.err }
