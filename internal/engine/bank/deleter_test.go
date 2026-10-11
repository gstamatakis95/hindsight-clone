package bank

import (
	"encoding/json"
	"errors"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
)

// seedBank fills b with a document, two units, a blob under its prefix, a pending consolidate job and a
// claimed retain job; it returns the pending and the claimed operation ids.
func seedBank(t *testing.T, e env, b port.BankRef) (pending, claimed port.OperationID) {
	t.Helper()
	e.tx(t, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, b); err != nil {
			return err
		}
		seq, err := tx.Seq(e.ctx, b)
		if err != nil {
			return err
		}
		doc := domain.Document{ID: "d1", Bank: b, BlobKey: domain.DocumentBlobKey(b, "d1", "h1")}
		if err := tx.Documents().Upsert(e.ctx, doc); err != nil {
			return err
		}
		units := []domain.Unit{
			{
				ID: pgtest.ID[port.UnitID]("u1"), Bank: b, DocumentID: "d1", Type: domain.FactTypeWorld,
				Text: "Alice likes tea", Seq: seq,
			},
			{
				ID: pgtest.ID[port.UnitID]("u2"), Bank: b, DocumentID: "d1", Type: domain.FactTypeWorld,
				Text: "Bob likes coffee", Seq: seq,
			},
		}
		if err := tx.Units().Insert(e.ctx, units); err != nil {
			return err
		}
		if _, err := tx.Directives().Upsert(e.ctx, domain.Directive{Bank: b, Name: "d", Active: true}); err != nil {
			return err
		}
		pending, _, err = tx.Queue().Enqueue(e.ctx, port.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate",
			Payload: json.RawMessage(`{}`), Dedupe: port.DedupePending,
		})
		if err != nil {
			return err
		}
		_, _, err = tx.Queue().Enqueue(e.ctx, port.Job{
			Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:d1", Payload: json.RawMessage(`{}`),
		})
		return err
	})
	if err := e.blob.Put(e.ctx, domain.DocumentBlobKey(b, "d1", "h1"), strings.NewReader("hello"), 5,
		"text/plain"); err != nil {
		t.Fatal(err)
	}
	jobs, err := e.store.Claim(e.ctx, "w1", []port.JobKind{domain.OperationKindRetainDocument}, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim retain: %v %v", jobs, err)
	}
	return pending, jobs[0].ID
}

// blobKeys returns the blob keys under prefix.
func blobKeys(e env, prefix string) []string {
	var out []string
	for _, k := range e.blob.Keys() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// claimDelete claims the bank's delete_bank job.
func claimDelete(t *testing.T, e env) port.Job {
	t.Helper()
	jobs, err := e.store.Claim(e.ctx, "w2", []port.JobKind{domain.OperationKindDeleteBank}, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim delete_bank: %v %v", jobs, err)
	}
	return jobs[0]
}

// assertWritesRefused checks every kind of write on a tombstoned bank fails with ErrBankDeleting.
func assertWritesRefused(t *testing.T, e env, b port.BankRef) {
	t.Helper()
	writes := map[string]func(tx port.Tx) error{
		"seq": func(tx port.Tx) error { _, err := tx.Seq(e.ctx, b); return err },
		"ensure": func(tx port.Tx) error {
			_, err := tx.Banks().Ensure(e.ctx, b)
			return err
		},
		"enqueue": func(tx port.Tx) error {
			_, _, err := tx.Queue().Enqueue(e.ctx, port.Job{
				Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Dedupe: port.DedupePending,
			})
			return err
		},
	}
	for name, w := range writes {
		if err := e.store.InTx(e.ctx, w); !errors.Is(err, domain.ErrBankDeleting) {
			t.Errorf("%s during delete: got %v, want ErrBankDeleting", name, err)
		}
	}
	_, err := NewDirectiveAdmin(e.store).UpsertDirective(e.ctx, domain.Directive{Bank: b, Name: "x"})
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("UpsertDirective during delete: got %v, want ErrBankDeleting", err)
	}
}

// assertReadsNotFound checks the bank's reads answer NOT_FOUND while it is being deleted.
func assertReadsNotFound(t *testing.T, e env, b port.BankRef) {
	t.Helper()
	if _, err := NewDirectiveAdmin(e.store).ListDirectives(e.ctx, b, nil, false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ListDirectives during delete: %v", err)
	}
	codec := NewTemplateCodec(e.store, refreshStub{})
	if _, err := codec.Export(e.ctx, b); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Export during delete: %v", err)
	}
	if _, err := NewDirectiveSelector(e.store).Select(e.ctx, b, nil, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Select during delete: %v", err)
	}
}

// runUntilDone runs the delete job until it reports done, counting the runs.
func runUntilDone(t *testing.T, e env, d BankDeleter, j port.Job) int {
	t.Helper()
	for runs := 1; runs <= 50; runs++ {
		done, err := d.Run(e.ctx, j)
		if err != nil {
			t.Fatalf("run %d: %v", runs, err)
		}
		if done {
			return runs
		}
	}
	t.Fatal("delete_bank never finished")
	return 0
}

// TestBankDeleteLifecycle: queued jobs never resurrect data, writes fail during the delete, and a write
// afterwards creates a fresh bank.
func TestBankDeleteLifecycle(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "doomed"}
	other := port.BankRef{Tenant: "t", Bank: "keep"}
	e.ensure(t, other)
	if err := e.blob.Put(e.ctx, domain.DocumentBlobKey(other, "d", "h"), strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	pending, claimed := seedBank(t, e, b)
	d := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{BatchSize: 1, MaxBatches: 2})

	op, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if op.Kind != domain.OperationKindDeleteBank || op.Status != domain.OperationStatusPending {
		t.Fatalf("tombstone op = %+v", op)
	}
	if got, _ := e.getOp(t, b, pending); got.Status != domain.OperationStatusCancelled {
		t.Errorf("pending consolidate after tombstone: %s, want cancelled", got.Status)
	}
	if alive, _ := e.store.Alive(e.ctx, claimed); alive {
		t.Error("claimed retain job still alive after tombstone")
	}
	assertWritesRefused(t, e, b)
	assertReadsNotFound(t, e, b)

	j := claimDelete(t, e)
	if j.ID != op.ID {
		t.Fatalf("claimed %s, want the tombstone's delete_bank %s", j.ID, op.ID)
	}
	if done, err := d.Run(e.ctx, j); err != nil || done {
		t.Fatalf("first run with a 2-batch budget: done=%v err=%v, want done=false", done, err)
	}
	assertBlockedByProcessing(t, e, d, j, claimed)
	// The retain handler reaches its checkpoint and stops; only then can the bank go.
	if err := e.store.Fail(e.ctx, claimed, errors.New("stopped at checkpoint"), nil); err != nil {
		t.Fatal(err)
	}
	if runs := runUntilDone(t, e, d, j); runs < 1 {
		t.Errorf("finished in %d more runs", runs)
	}

	if keys := blobKeys(e, domain.BankBlobPrefix(b)); len(keys) != 0 {
		t.Errorf("blobs left under the bank prefix: %v", keys)
	}
	if keys := blobKeys(e, domain.BankBlobPrefix(other)); len(keys) != 1 {
		t.Errorf("other bank's blobs touched: %v", keys)
	}
	assertFreshBank(t, e, b, pending, claimed)
	// The old retain job ended before the bank went, so it can't settle (or write) into the fresh bank.
	if err := e.store.Complete(e.ctx, claimed, nil, nil); err == nil {
		t.Error("old retain job completed against the fresh bank")
	}
}

// assertBlockedByProcessing runs the delete while the claimed job of the bank is still processing: every
// run reports done=false, the bank stays tombstoned with its blobs, the job's writes are refused and a
// delayed delete_bank continuation is queued.
func assertBlockedByProcessing(t *testing.T, e env, d BankDeleter, j port.Job, claimed port.OperationID) {
	t.Helper()
	var cont *domain.Operation
	for runs := 0; runs < 50 && cont == nil; runs++ {
		done, err := d.Run(e.ctx, j)
		if err != nil || done {
			t.Fatalf("run while a job is processing: done=%v err=%v, want done=false", done, err)
		}
		cont = delayedContinuation(t, e, j)
	}
	if cont == nil {
		t.Fatal("no delayed delete_bank continuation while a job is processing")
	}
	if want := e.clock.Now().Add(DeleteBlockedDelay); cont.NextRunAt == nil || cont.NextRunAt.Before(want) {
		t.Errorf("continuation run_at = %v, want >= %v", cont.NextRunAt, want)
	}
	if done, err := d.Run(e.ctx, j); err != nil || done {
		t.Errorf("run still blocked: done=%v err=%v", done, err)
	}
	var bank domain.Bank
	e.tx(t, func(tx port.Tx) error { var err error; bank, err = tx.Banks().Get(e.ctx, j.Bank); return err })
	if !bank.Deleting() || len(blobKeys(e, domain.BankBlobPrefix(j.Bank))) == 0 {
		t.Errorf("blocked delete went ahead: bank %+v, blobs %v", bank, blobKeys(e, domain.BankBlobPrefix(j.Bank)))
	}
	err := e.store.InTx(e.ctx, func(tx port.Tx) error { _, err := tx.Seq(e.ctx, j.Bank); return err })
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("claimed job %s write while blocked: %v, want ErrBankDeleting", claimed, err)
	}
}

// delayedContinuation returns the pending delete_bank operation other than j, if any.
func delayedContinuation(t *testing.T, e env, j port.Job) *domain.Operation {
	t.Helper()
	var out *domain.Operation
	e.tx(t, func(tx port.Tx) error {
		out = nil
		ops, _, err := tx.Queue().List(e.ctx, j.Bank, port.OperationFilter{
			Status: domain.OperationStatusPending, Kind: domain.OperationKindDeleteBank, PageSize: 100,
		})
		for i := range ops {
			if ops[i].ID != j.ID {
				out = &ops[i]
			}
		}
		return err
	})
	return out
}

// assertFreshBank checks bank b is gone with its jobs, and a write afterwards creates a clean bank.
func assertFreshBank(t *testing.T, e env, b port.BankRef, oldOps ...port.OperationID) {
	t.Helper()
	err := e.store.InTx(e.ctx, func(tx port.Tx) error { _, err := tx.Banks().Get(e.ctx, b); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bank after delete: %v, want NotFound", err)
	}
	for _, id := range oldOps {
		if alive, _ := e.store.Alive(e.ctx, id); alive {
			t.Errorf("old job %s alive after delete", id)
		}
		err := e.store.InTx(e.ctx, func(tx port.Tx) error { _, err := tx.Queue().Retry(e.ctx, b, id); return err })
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("retry of old job %s: %v, want NotFound (the row went with the bank)", id, err)
		}
	}
	var seq int64
	e.tx(t, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, b); err != nil {
			return err
		}
		var err error
		seq, err = tx.Seq(e.ctx, b)
		return err
	})
	if seq != 1 {
		t.Errorf("fresh bank's first seq = %d, want 1", seq)
	}
	units, err := e.store.Read().GetUnits(e.ctx, b, pgtest.IDs[port.UnitID]("u1", "u2"))
	if err != nil || len(units) != 0 {
		t.Errorf("fresh bank units = %v (%v), want none", units, err)
	}
	err = e.store.InTx(e.ctx, func(tx port.Tx) error { _, err := tx.Documents().Get(e.ctx, b, "d1"); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("fresh bank document: %v, want NotFound", err)
	}
	dirs, err := NewDirectiveAdmin(e.store).ListDirectives(e.ctx, b, nil, false)
	if err != nil || len(dirs) != 0 {
		t.Errorf("fresh bank directives = %v (%v), want none", dirs, err)
	}
}

func TestTombstoneIdempotent(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	d := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{})
	if _, err := d.Tombstone(e.ctx, b); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown bank: %v, want NotFound", err)
	}
	e.ensure(t, b)
	first, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("second tombstone enqueued %s, want the first %s", second.ID, first.ID)
	}
	claimDelete(t, e)
	third, err := d.Tombstone(e.ctx, b)
	if err != nil || third.ID != first.ID {
		t.Errorf("tombstone while running: %s %v, want %s joined", third.ID, err, first.ID)
	}
}

func TestDeleteRunSkipsPrefixWhenBankGone(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	seedBank(t, e, b)
	d := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{})
	if _, err := d.Tombstone(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	// An earlier run finished (bank row gone), then a write created a fresh bank with a new blob.
	e.tx(t, func(tx port.Tx) error { return tx.Banks().Delete(e.ctx, b) })
	if done, err := d.Run(e.ctx, j); err != nil || !done {
		t.Fatalf("run on a gone bank: done=%v err=%v", done, err)
	}
	e.ensure(t, b)
	fresh := domain.DocumentBlobKey(b, "new", "h")
	if err := e.blob.Put(e.ctx, fresh, strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	if done, err := d.Run(e.ctx, j); err != nil || !done {
		t.Fatalf("run on a fresh bank: done=%v err=%v", done, err)
	}
	// The bank row was removed by hand above, so the old blob is still there too: Run deleted nothing.
	if keys := blobKeys(e, domain.BankBlobPrefix(b)); len(keys) != 2 || !slices.Contains(keys, fresh) {
		t.Errorf("blobs under the prefix = %v, want both kept (incl. %s)", keys, fresh)
	}
}

func TestDeleteRunStopsWhenCancelled(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	e.ensure(t, b)
	key := domain.DocumentBlobKey(b, "d", "h")
	if err := e.blob.Put(e.ctx, key, strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	d := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{})
	op, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	e.tx(t, func(tx port.Tx) error { _, err := tx.Queue().Cancel(e.ctx, b, op.ID); return err })
	if _, err := d.Run(e.ctx, j); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("run after cancel: %v, want ErrCancelled", err)
	}
	if keys := blobKeys(e, domain.BankBlobPrefix(b)); len(keys) != 1 {
		t.Errorf("a cancelled delete removed blobs: %v", keys)
	}
}
