package memory

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
	"time"
)

// batch runs RetainBatch on testBank and fails the test on a call error.
func (e *env) batch(items ...RetainItem) RetainBatchResult {
	e.t.Helper()
	res, err := e.svc.RetainBatch(e.ctx, RetainBatchRequest{Bank: testBank, Items: items})
	if err != nil {
		e.t.Fatalf("RetainBatch: %v", err)
	}
	if len(res.Items) != len(items) {
		e.t.Fatalf("RetainBatch: %d results for %d items", len(res.Items), len(items))
	}
	return res
}

// statuses lists the results' statuses in order.
func statuses(res RetainBatchResult) []BatchStatus {
	out := make([]BatchStatus, len(res.Items))
	for k, it := range res.Items {
		out[k] = it.Status
	}
	return out
}

// runRetain claims the next retain_document job, runs it and marks it completed.
func (e *env) runRetain() {
	e.t.Helper()
	j := e.claimRetain()
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); err != nil {
		e.t.Fatalf("ProcessDocument: %v", err)
	}
	if err := e.store.Complete(e.ctx, j.ID, nil, nil); err != nil {
		e.t.Fatalf("Complete: %v", err)
	}
}

// one runs RetainBatch with a single item and returns its status.
func (e *env) one(item RetainItem) BatchStatus {
	e.t.Helper()
	return e.batch(item).Items[0].Status
}

// wantDoc checks a document's stored revision and that its blob holds content.
func (e *env) wantDoc(id domain.DocumentID, revision int64, content string) {
	e.t.Helper()
	d := e.document(id)
	if d.Revision == nil || *d.Revision != revision || !strings.HasSuffix(d.BlobKey, contentHash(content)) {
		e.t.Errorf("document %s: revision %v, blob %s; want revision %d holding %q", id, d.Revision, d.BlobKey,
			revision, content)
	}
}

// TestRetainBatchItemStatuses: every item gets its own status and one bad item never fails the call. Within the
// call the highest revision of a document wins (the last among equals); against the stored document a lower
// revision is STALE and changes nothing, while a higher one is accepted and stored.
func TestRetainBatchItemStatuses(t *testing.T) {
	e := newEnv(t, 0)
	e.ingestor.d.Limits.MaxDocumentBytes = 64
	if got := e.one(RetainItem{DocumentID: "d1", Content: "version five", Revision: ptr[int64](5)}); got != BatchAccepted {
		t.Fatalf("seed: %v", got)
	}
	bad := &domain.BankConfig{Retain: &domain.RetainConfig{ExtractionMode: domain.ExtractionModeCustom}} // no prompt
	res := e.batch(
		RetainItem{DocumentID: "d1", Content: "version three", Revision: ptr[int64](3)}, // older than stored 5
		RetainItem{DocumentID: "d2", Content: "two at 2", Revision: ptr[int64](2)},
		RetainItem{DocumentID: "d2", Content: "two at 1", Revision: ptr[int64](1)}, // later but lower
		RetainItem{DocumentID: "d3", Content: "three first"},
		RetainItem{DocumentID: "d3", Content: "three last", Revision: ptr[int64](1)}, // last occurrence wins
		RetainItem{Content: "no document id"},
		RetainItem{DocumentID: "d4", Content: strings.Repeat("x", 65)},
		RetainItem{DocumentID: "d5", Content: "bad overrides", Overrides: bad},
	)
	want := []BatchStatus{
		BatchStale, BatchAccepted, BatchStale, BatchSuperseded, BatchAccepted, BatchRejected, BatchRejected,
		BatchRejected,
	}
	if got := statuses(res); !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	for k, it := range res.Items {
		if (it.Status == BatchAccepted) != (it.Operation != nil) {
			t.Errorf("item %d: status %v with operation %v", k, it.Status, it.Operation)
		}
		if (it.Status == BatchRejected) != errors.Is(it.Err, domain.ErrInvalidArgument) {
			t.Errorf("item %d: status %v with error %v", k, it.Status, it.Err)
		}
	}
	if v := domain.FieldViolations(res.Items[5].Err); len(v) != 1 || v[0].Field != "document_id" {
		t.Errorf("item 5 field violations = %+v, want document_id", v)
	}
	e.wantDoc("d1", 5, "version five")
	e.wantDoc("d2", 2, "two at 2")
	e.wantDoc("d3", 1, "three last")
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 3 {
		t.Errorf("%d retain_document jobs, want 3 (seed, d2, d3)", len(ops))
	}
	if got := e.one(RetainItem{DocumentID: "d1", Content: "version 7", Revision: ptr[int64](7)}); got != BatchAccepted {
		t.Errorf("newer revision: %v, want ACCEPTED", got)
	}
	if got := e.one(RetainItem{DocumentID: "d1", Content: "version six", Revision: ptr[int64](6)}); got != BatchStale {
		t.Errorf("revision 6 after 7: %v, want STALE", got)
	}
	e.wantDoc("d1", 7, "version 7")
}

// TestRetainBatchRedeliveryIsIdempotent: a redelivered identical item joins the retain in flight (same operation,
// no new job) and, once that retain completed, is UNCHANGED: no model call, no job, no seq bump.
func TestRetainBatchRedeliveryIsIdempotent(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	item := RetainItem{DocumentID: "d1", Content: "Alice likes tea.", Revision: ptr[int64](1), Tags: []string{"t"}}
	first := e.batch(item)
	again := e.batch(item)
	if again.Items[0].Status != BatchAccepted || again.Items[0].Operation == nil ||
		again.Items[0].Operation.ID != first.Items[0].Operation.ID {
		t.Fatalf("redelivery in flight = %+v, want ACCEPTED joining %s", again.Items[0], first.Items[0].Operation.ID)
	}
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 1 {
		t.Fatalf("%d retain_document jobs after a redelivery in flight, want 1", len(ops))
	}
	e.runRetain()
	calls := e.extractCalls()
	seq, _ := e.store.Read().BankSeq(e.ctx, testBank)

	done := e.batch(item)
	if done.Items[0].Status != BatchUnchanged || done.Items[0].Operation != nil {
		t.Errorf("redelivery after completion = %+v, want UNCHANGED", done.Items[0])
	}
	if got := e.extractCalls(); got != calls {
		t.Errorf("extract calls %d -> %d on a redelivery", calls, got)
	}
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 1 {
		t.Errorf("%d retain_document jobs, want 1", len(ops))
	}
	if s, _ := e.store.Read().BankSeq(e.ctx, testBank); s != seq {
		t.Errorf("bank seq moved %d -> %d on a redelivery", seq, s)
	}
}

// TestRetainBatchBacklogWritesNothing: over the bank's or the org's pending retain backlog, the whole call fails
// with INGEST_BACKLOG (RESOURCE_EXHAUSTED) carrying the retry delay, and writes nothing: no blob, document, job,
// seq bump or bank.
func TestRetainBatchBacklogWritesNothing(t *testing.T) {
	e := newEnv(t, 0)
	e.batch(RetainItem{DocumentID: "a", Content: "first"}, RetainItem{DocumentID: "b", Content: "second"})
	blobs := len(e.blob.Keys())
	seq, _ := e.store.Read().BankSeq(e.ctx, testBank)
	other := domain.NewBankRef(testBank.Tenant, testBank.Org, "other")
	limits := e.ingestor.d.Limits
	for _, tc := range []struct {
		name string
		bank domain.BankRef
		set  func(*IngestLimits)
	}{
		{"bank", testBank, func(l *IngestLimits) { l.MaxPendingPerBank = 1 }},
		{"org", other, func(l *IngestLimits) { l.MaxPendingPerOrg = 1 }},
	} {
		e.ingestor.d.Limits = limits
		tc.set(&e.ingestor.d.Limits)
		e.ingestor.d.Limits.RetryAfter = 7 * time.Second
		_, err := e.svc.RetainBatch(e.ctx, RetainBatchRequest{Bank: tc.bank, Items: []RetainItem{
			{DocumentID: "c", Content: "third"},
		}})
		if !errors.Is(err, domain.ErrIngestBacklog) || !errors.Is(err, domain.ErrResourceExhausted) {
			t.Fatalf("%s: err = %v, want INGEST_BACKLOG", tc.name, err)
		}
		if d, ok := domain.RetryDelay(err); !ok || d != 7*time.Second {
			t.Errorf("%s: retry delay = %v %v, want 7s", tc.name, d, ok)
		}
	}
	if n := len(e.blob.Keys()); n != blobs {
		t.Errorf("blobs %d -> %d", blobs, n)
	}
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 2 {
		t.Errorf("%d retain_document jobs, want 2", len(ops))
	}
	if s, _ := e.store.Read().BankSeq(e.ctx, testBank); s != seq {
		t.Errorf("bank seq moved %d -> %d", seq, s)
	}
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		if _, err := tx.Documents().Get(e.ctx, testBank, "c"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("document c: %v, want not found", err)
		}
		if _, err := tx.Banks().Get(e.ctx, other); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("bank other: %v, want not created", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// raceRetain leaves d1 in flight, runs preRead (an item's pre-read, which finds d1 changed), then completes d1's
// retain. The returned check, called after the item's write, asserts the write changed nothing.
func raceRetain(t *testing.T, item RetainItem, preRead func(e *env)) (*env, func()) {
	t.Helper()
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	e.batch(item)
	j := e.claimRetain()
	preRead(e)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); err != nil {
		t.Fatal(err)
	}
	completed, calls := e.document("d1").ExtractedAt, e.extractCalls()
	return e, func() {
		t.Helper()
		if d := e.document("d1"); d.ExtractedAt == nil || !d.ExtractedAt.Equal(*completed) {
			t.Errorf("extracted_at %v -> %v: the write reopened a completed document", completed, d.ExtractedAt)
		}
		if got := e.extractCalls(); got != calls {
			t.Errorf("extract calls %d -> %d", calls, got)
		}
		if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 1 {
			t.Errorf("%d retain_document jobs, want 1", len(ops))
		}
	}
}

// TestRetainNoopRecheckUnderSeq: a retain that completes between an item's pre-read and its write transaction
// makes the item a no-op under the Seq lock, for Retain and RetainBatch alike: the upsert does not clear the
// fresh extracted_at (which would force a paid re-extraction) and nothing is enqueued.
func TestRetainNoopRecheckUnderSeq(t *testing.T) {
	item := RetainItem{DocumentID: "d1", Content: "Alice likes tea."}
	t.Run("Retain", func(t *testing.T) {
		req := RetainRequest{Bank: testBank, RunAsync: true, Items: []RetainItem{item}}
		var plans []*itemPlan
		e, check := raceRetain(t, item, func(e *env) {
			p, err := e.ingestor.plan(e.ctx, req, item, domain.BankConfig{}, true)
			if err != nil {
				t.Fatal(err)
			}
			plans = []*itemPlan{p}
			if err := e.ingestor.preRead(e.ctx, testBank, plans); err != nil || plans[0].pre != outcomeWrite {
				t.Fatalf("pre-read: %v, outcome %d; want the in-flight document changed", err, plans[0].pre)
			}
		})
		res, err := e.ingestor.write(e.ctx, req, plans)
		if err != nil || !res.Documents[0].Unchanged || res.Documents[0].Operation != nil {
			t.Errorf("write = %+v, %v; want unchanged with no operation", res.Documents, err)
		}
		check()
	})
	t.Run("RetainBatch", func(t *testing.T) {
		req := RetainBatchRequest{Bank: testBank, Items: []RetainItem{item}}
		var items []*batchItem
		e, check := raceRetain(t, item, func(e *env) {
			items = e.ingestor.planBatch(e.ctx, req, domain.BankConfig{}, true)
			if err := e.ingestor.preReadBatch(e.ctx, testBank, items); err != nil || !items[0].pending() {
				t.Fatalf("pre-read: %v, %+v; want the in-flight document pending", err, items[0].res)
			}
		})
		e.ingestor.writeBatch(e.ctx, testBank, items)
		if items[0].res.Status != BatchUnchanged {
			t.Errorf("status = %v (%v), want UNCHANGED", items[0].res.Status, items[0].res.Err)
		}
		check()
	})
}
