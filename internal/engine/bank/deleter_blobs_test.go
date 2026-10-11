package bank

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
)

// listerStub lists a fake.Blob's keys in order (the BlobLister).
type listerStub struct{ blob *fake.Blob }

// List returns up to limit keys under prefix sorting after after.
func (l listerStub) List(_ context.Context, prefix, after string, limit int) ([]string, error) {
	keys := l.blob.Keys()
	slices.Sort(keys)
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) && k > after && len(out) < limit {
			out = append(out, k)
		}
	}
	return out, nil
}

// hookBlob is a fake.Blob whose first Delete runs hook afterwards.
type hookBlob struct {
	*fake.Blob
	hook func()
}

// Delete deletes key, then runs the hook once.
func (h *hookBlob) Delete(ctx context.Context, key string) error {
	err := h.Blob.Delete(ctx, key)
	if h.hook != nil {
		hook := h.hook
		h.hook = nil
		hook()
	}
	return err //nolint:wrapcheck // test double passes the store's error through
}

// TestDeletePagesBlobsWithFencing: with a BlobLister the blobs go page by page, and a bank that is no
// longer tombstoned between pages (an earlier run finished and a write recreated it) stops the deletes.
func TestDeletePagesBlobsWithFencing(t *testing.T) {
	e := newEnv(t)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	e.ensure(t, b)
	var keys []string
	for _, doc := range []port.DocumentID{"a", "b", "c"} {
		k := domain.DocumentBlobKey(b, doc, "h")
		keys = append(keys, k)
		if err := e.blob.Put(e.ctx, k, strings.NewReader("x"), 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	blob := &hookBlob{Blob: e.blob, hook: func() {
		e.tx(t, func(tx port.Tx) error { return tx.Banks().Delete(e.ctx, b) })
		e.ensure(t, b)
	}}
	d := NewBankDeleter(e.store, blob, e.store, e.clock, DeleterConfig{Blobs: listerStub{e.blob}, BlobPageSize: 1})
	if _, err := d.Tombstone(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	// The bank row went with this job's own operation, so the between-page fence stops the run (Alive is
	// false: ErrCancelled); a duplicate run whose row survived would stop on the tombstone re-check instead.
	if done, err := d.Run(e.ctx, j); done || !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("run: done=%v err=%v, want stopped with ErrCancelled", done, err)
	}
	if got := blobKeys(e, domain.BankBlobPrefix(b)); !slices.Equal(got, keys[1:]) {
		t.Errorf("blobs left = %v, want %v (only the first page deleted)", got, keys[1:])
	}
	e.tx(t, func(tx port.Tx) error {
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
	b := port.BankRef{Tenant: "t", Bank: "half"}
	_, claimed := seedBank(t, e, b)
	if err := e.store.Fail(e.ctx, claimed, errors.New("stopped"), nil); err != nil {
		t.Fatal(err)
	}
	d := NewBankDeleter(e.store, e.blob, e.store, e.clock, DeleterConfig{BatchSize: 1, MaxBatches: 1})
	first, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	j := claimDelete(t, e)
	if done, err := d.Run(e.ctx, j); err != nil || done {
		t.Fatalf("first run: done=%v err=%v, want rows left", done, err)
	}
	if err := e.store.Fail(e.ctx, j.ID, errors.New("gave up"), nil); err != nil {
		t.Fatal(err)
	}
	again, err := d.Tombstone(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == first.ID || again.Status != domain.OperationStatusPending {
		t.Fatalf("re-delete op = %+v, want a new pending delete_bank (first %s)", again, first.ID)
	}
	j2 := claimDelete(t, e)
	runUntilDone(t, e, d, j2)
	if keys := blobKeys(e, domain.BankBlobPrefix(b)); len(keys) != 0 {
		t.Errorf("blobs left: %v", keys)
	}
	err = e.store.InTx(e.ctx, func(tx port.Tx) error { _, err := tx.Banks().Get(e.ctx, b); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bank after re-delete: %v, want NotFound", err)
	}
}
