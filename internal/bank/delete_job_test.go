package bank

import (
	"context"
	"errors"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"strings"
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

func (t failingDeletesTx) Banks() store.BankRepo {
	return failingBatch{BankRepo: t.Tx.Banks(), err: t.err}
}

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

// hookBlob is a fake.Blob whose first DeletePrefix runs hook afterwards.
type hookBlob struct {
	*fake.Blob
	hook func()
}

// DeletePrefix deletes the keys under prefix, then runs the hook once.
func (h *hookBlob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	n, err := h.Blob.DeletePrefix(ctx, prefix)
	if h.hook != nil {
		hook := h.hook
		h.hook = nil
		hook()
	}
	return n, err //nolint:wrapcheck // test double passes the store's error through
}

// TestDeletePagesBlobsWithFencing: the blobs go page by page, and a bank that is no
// longer tombstoned between pages (an earlier run finished and a write recreated it) stops the deletes.
func TestDeletePagesBlobsWithFencing(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	e.ensure(t, b)
	var keys []string
	for _, doc := range []domain.DocumentID{"a", "b", "c"} {
		k := blob.Key(b, doc, "h")
		keys = append(keys, k)
		if err := e.blob.Put(e.ctx, k, strings.NewReader("x"), 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	hooked := &hookBlob{Blob: e.blob, hook: func() {
		e.tx(t, func(tx store.Tx) error { return tx.Banks().Delete(e.ctx, b) })
		e.ensure(t, b)
	}}
	d := e.svcOver(e.store, hooked, DeleteConfig{BlobPageSize: 1})
	if _, err := d.DeleteBank(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	// The bank row went with this job's own operation, so the between-page fence stops the run (Alive is
	// false: ErrCancelled); a duplicate run whose row survived would stop on the tombstone re-check instead.
	if done, err := d.runDelete(e.ctx, jobs.NewRun(e.store, j)); done || !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("run: done=%v err=%v, want stopped with ErrCancelled", done, err)
	}
	if got := blobKeys(e, blob.BankPrefix(b)); !slices.Equal(got, keys[1:]) {
		t.Errorf("blobs left = %v, want %v (only the first page deleted)", got, keys[1:])
	}
	e.tx(t, func(tx store.Tx) error {
		bank, err := tx.Banks().Get(e.ctx, b)
		if err == nil && bank.Deleting() {
			t.Error("fresh bank deleted or tombstoned")
		}
		return err
	})
}

// TestRedeleteResumesHalfDeletedBank: a delete_bank that failed for good leaves a tombstoned, half-deleted
// bank; re-issuing DeleteBank enqueues a new delete_bank that finishes it.
func TestRedeleteResumesHalfDeletedBank(t *testing.T) {
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t", Bank: "half"}
	_, claimed := seedBank(t, e, b)
	if err := e.store.Fail(e.ctx, claimed, errors.New("stopped"), nil); err != nil {
		t.Fatal(err)
	}
	d := e.svcOver(e.store, e.blob, DeleteConfig{BatchSize: 1, MaxBatches: 1})
	first, err := d.DeleteBank(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	if done, err := d.runDelete(e.ctx, jobs.NewRun(e.store, j)); err != nil || done {
		t.Fatalf("first run: done=%v err=%v, want rows left", done, err)
	}
	if err := e.store.Fail(e.ctx, j.ID, errors.New("gave up"), nil); err != nil {
		t.Fatal(err)
	}
	again, err := d.DeleteBank(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == first.ID || again.Status != domain.OperationStatusPending {
		t.Fatalf("re-delete op = %+v, want a new pending delete_bank (first %s)", again, first.ID)
	}
	j2 := claimDelete(t, e)
	runUntilDone(t, e, d, j2)
	if keys := blobKeys(e, blob.BankPrefix(b)); len(keys) != 0 {
		t.Errorf("blobs left: %v", keys)
	}
	err = e.store.InTx(e.ctx, func(tx store.Tx) error { _, err := tx.Banks().Get(e.ctx, b); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bank after re-delete: %v, want NotFound", err)
	}
}
