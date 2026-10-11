package jobs

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
	"time"
)

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
	ops.markDeleting(domain.BankRef{Tenant: "t9", Bank: "gone"})
	rows := ops.count("true")
	tests := []struct {
		bank domain.BankRef
		want bool
	}{
		{bankA, true},  // due automatic consolidate
		{bankB, false}, // parked
		{bankC, false}, // not joinable
		{domain.BankRef{Tenant: "t9", Bank: "gone"}, false}, // tombstoned
	}
	for _, tt := range tests {
		if ok, err := pc.hasPending(bg, tt.bank); err != nil || ok != tt.want {
			t.Errorf("bank %v = %v, %v; want %v", tt.bank, ok, err, tt.want)
		}
	}
	if _, err := pc.hasPending(bg, domain.BankRef{Tenant: "t9", Bank: "x"}); !errors.Is(
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
