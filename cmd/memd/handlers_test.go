package main

import (
	"context"
	"errors"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"

	mockbank "memd/internal/mocks/engine/bank"
)

// hBank is the bank the handler tests use.
var hBank = domain.BankRef{Tenant: "t", Bank: "b"}

// handlerEnv is a store over the test's database with hBank created, and a clock frozen at the start.
type handlerEnv struct {
	store *postgres.DB
	clock *clock.Manual
}

func newHandlerEnv(t *testing.T) handlerEnv {
	t.Helper()
	s := pgtest.NewStore(t)
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		_, err := tx.Banks().Ensure(t.Context(), hBank)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return handlerEnv{store: s, clock: clock.NewManual(time.Now().UTC().Truncate(time.Microsecond))}
}

// ops lists the bank's operations of kind, newest first.
func (e handlerEnv) ops(t *testing.T, kind domain.OperationKind) []domain.Operation {
	t.Helper()
	var out []domain.Operation
	if err := e.store.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		out, _, err = tx.Queue().List(t.Context(), hBank, store.OperationFilter{Kind: kind, PageSize: 100})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDeleteBankReenqueuesUntilDone checks a run with work left queues one continuation (DedupePending), and a
// done run queues none.
func TestDeleteBankReenqueuesUntilDone(t *testing.T) {
	e := newHandlerEnv(t)
	done := false
	h := handlers{db: e.store, clock: e.clock, deleter: &mockbank.BankDeleter{
		RunFunc: func(context.Context, *jobs.Run) (bool, error) { return done, nil },
	}}
	job := store.Job{Bank: hBank, Kind: domain.OperationKindDeleteBank, Payload: []byte(`{}`)}
	for range 2 {
		if err := h.deleteBank(t.Context(), jobs.Inline(job)); err != nil {
			t.Fatal(err)
		}
	}
	if ops := e.ops(t, domain.OperationKindDeleteBank); len(ops) != 1 || ops[0].SerialKey != "bank" {
		t.Fatalf("continuations: %+v, want one pending (second joined)", ops)
	}
	done = true
	if err := h.deleteBank(t.Context(), jobs.Inline(job)); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(t, domain.OperationKindDeleteBank); len(ops) != 1 {
		t.Fatalf("a done run queued more: %+v", ops)
	}
}

// TestDeleteBankRetriesWithoutAttemptCap checks a transient failure on the last capped attempt still leaves a
// delayed continuation, so the bank delete is never abandoned; earlier attempts and permanent errors leave
// the retry to the runner.
func TestDeleteBankRetriesWithoutAttemptCap(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		err     error
		want    int
	}{
		{"first attempt: runner retries", 0, errors.New("db down"), 0},
		{"last attempt: continuation", domain.DefaultMaxAttempts - 1, errors.New("db down"), 1},
		{"permanent: no continuation", domain.DefaultMaxAttempts - 1, domain.ErrInvalidArgument, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newHandlerEnv(t)
			h := handlers{db: e.store, clock: e.clock, deleter: &mockbank.BankDeleter{
				RunFunc: func(context.Context, *jobs.Run) (bool, error) { return false, tc.err },
			}}
			job := store.Job{Bank: hBank, Kind: domain.OperationKindDeleteBank, Attempt: tc.attempt}
			if err := h.deleteBank(t.Context(), jobs.Inline(job)); !errors.Is(err, tc.err) {
				t.Fatalf("error %v, want %v", err, tc.err)
			}
			ops := e.ops(t, domain.OperationKindDeleteBank)
			if len(ops) != tc.want {
				t.Fatalf("%d continuations, want %d", len(ops), tc.want)
			}
			if tc.want == 1 && (ops[0].NextRunAt == nil || !ops[0].NextRunAt.After(e.clock.Now())) {
				t.Fatalf("continuation not delayed: %+v", ops[0])
			}
		})
	}
}
