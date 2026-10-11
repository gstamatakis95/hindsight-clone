package bank

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
)

// failingDeletes is a store whose BankRepo.DeleteBatch fails with err.
type failingDeletes struct {
	store.DB
	err error
}

func (s failingDeletes) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error { return fn(failingDeletesTx{Tx: tx, err: s.err}) })
}

type failingDeletesTx struct {
	store.Tx
	err error
}

func (t failingDeletesTx) Banks() store.BankRepo { return failingBatch{BankRepo: t.Tx.Banks(), err: t.err} }

type failingBatch struct {
	store.BankRepo
	err error
}

func (f failingBatch) DeleteBatch(context.Context, domain.BankRef, int) (int, error) { return 0, f.err }

// deleteOps lists bank b's delete_bank operations.
func (e env) deleteOps(t *testing.T, b domain.BankRef) []domain.Operation {
	t.Helper()
	var out []domain.Operation
	e.tx(t, func(tx store.Tx) error {
		var err error
		out, _, err = tx.Queue().List(e.ctx, b, store.OperationFilter{
			Kind: domain.OperationKindDeleteBank, PageSize: 100,
		})
		return err
	})
	return out
}

// TestRunDeleteJobReenqueuesUntilDone: a run that leaves work queues one continuation (a second joins it), and a
// run that finishes queues none.
func TestRunDeleteJobReenqueuesUntilDone(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	seedBank(t, e, b)
	s := e.svc(DeleteConfig{BatchSize: 1, MaxBatches: 1})
	if _, err := s.DeleteBank(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	job := claimDelete(t, e)
	for range 2 {
		if err := s.RunDeleteJob(e.ctx, jobs.NewRun(e.store, job)); err != nil {
			t.Fatal(err)
		}
	}
	var pending []domain.Operation
	for _, op := range e.deleteOps(t, b) {
		if op.Status == domain.OperationStatusPending {
			pending = append(pending, op)
		}
	}
	if len(pending) != 1 || pending[0].SerialKey != jobs.SerialKeyDeleteBank {
		t.Fatalf("continuations: %+v, want one pending (second joined)", pending)
	}
	empty := domain.BankRef{Tenant: "t", Bank: "empty"} // not tombstoned: the run is done at once
	e.ensure(t, empty)
	job = store.Job{Bank: empty, Kind: domain.OperationKindDeleteBank, Payload: []byte(`{}`)}
	if err := s.RunDeleteJob(e.ctx, jobs.Inline(job)); err != nil {
		t.Fatal(err)
	}
	if ops := e.deleteOps(t, empty); len(ops) != 0 {
		t.Fatalf("a done run queued more: %+v", ops)
	}
}

// TestRunDeleteJobRetriesWithoutAttemptCap: a transient failure on the last capped attempt still leaves a delayed
// continuation, so the bank delete is never abandoned; earlier attempts and permanent errors leave the retry to the
// runner.
func TestRunDeleteJobRetriesWithoutAttemptCap(t *testing.T) {
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
			e := newEnv(t)
			b := domain.BankRef{Tenant: "t", Bank: "b"}
			e.ensure(t, b)
			s := e.svcOver(failingDeletes{DB: e.store, err: tc.err}, e.blob, DeleteConfig{})
			job := store.Job{Bank: b, Kind: domain.OperationKindDeleteBank, Attempt: tc.attempt}
			if err := s.RunDeleteJob(e.ctx, jobs.Inline(job)); !errors.Is(err, tc.err) {
				t.Fatalf("error %v, want %v", err, tc.err)
			}
			ops := e.deleteOps(t, b)
			if len(ops) != tc.want {
				t.Fatalf("%d continuations, want %d", len(ops), tc.want)
			}
			if tc.want == 1 && (ops[0].NextRunAt == nil || !ops[0].NextRunAt.After(e.clock.Now())) {
				t.Fatalf("continuation not delayed: %+v", ops[0])
			}
		})
	}
}
