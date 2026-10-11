package memory

import (
	"encoding/json"
	"errors"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"testing"
	"time"
)

// TestIngestUnchangedMakesNoLLMCall: re-retaining the same content with the same overrides is a no-op: no
// model call, no job, no write (extracted_at stays set). force_reextract and changed overrides re-run.
func TestIngestUnchangedMakesNoLLMCall(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	item := RetainItem{
		DocumentID: "d1", Content: "Alice likes tea.", Overrides: &domain.BankConfig{
			Retain: &domain.RetainConfig{Mission: "m"},
		},
	}
	e.ingest(RetainRequest{Items: []RetainItem{item}})
	before := e.document("d1")
	seq, _ := e.store.Read().BankSeq(e.ctx, testBank)
	e.llm.Reset()

	for _, async := range []bool{false, true} {
		res := e.ingest(RetainRequest{RunAsync: async, Items: []RetainItem{item}})
		if !res.Documents[0].Unchanged || res.Documents[0].Operation != nil {
			t.Errorf("async=%v: result %+v, want unchanged with no operation", async, res.Documents[0])
		}
	}
	if calls := e.llm.Calls(); len(calls) != 0 {
		t.Errorf("unchanged re-retain made %d LLM calls, want 0", len(calls))
	}
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 0 {
		t.Errorf("unchanged re-retain enqueued %d jobs", len(ops))
	}
	after := e.document("d1")
	if after.ExtractedAt == nil || !after.ExtractedAt.Equal(*before.ExtractedAt) || after.MemoryCount != 1 {
		t.Errorf("document changed: %+v -> %+v", before, after)
	}
	if s, _ := e.store.Read().BankSeq(e.ctx, testBank); s != seq {
		t.Errorf("bank seq moved %d -> %d on a no-op", seq, s)
	}

	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	forced := item
	forced.ForceReextract = true
	if res := e.ingest(RetainRequest{Items: []RetainItem{forced}}); res.Documents[0].Unchanged {
		t.Error("force_reextract reported unchanged")
	}
	changed := item
	changed.Overrides = &domain.BankConfig{Retain: &domain.RetainConfig{Mission: "other"}}
	if res := e.ingest(RetainRequest{Items: []RetainItem{changed}}); res.Documents[0].Unchanged {
		t.Error("changed overrides reported unchanged")
	}
	if got := e.extractCalls(); got != 2 {
		t.Errorf("%d extract calls for force + changed overrides, want 2", got)
	}
	if n := e.document("d1").MemoryCount; n != 1 {
		t.Errorf("%d units after replacements, want 1", n)
	}
}

// TestIngestFanOutRejectionStoresNothing: an ALL_COMBINATIONS fan-out over max_combinations fails the whole
// request with INVALID_ARGUMENT before any blob, document, unit or job is written.
func TestIngestFanOutRejectionStoresNothing(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	e.inTx(func(store.Tx) error { return nil }) // creates the bank
	seq, _ := e.store.Read().BankSeq(e.ctx, testBank)
	limit := 7
	for _, async := range []bool{false, true} {
		_, err := e.ingestor.Ingest(e.ctx, RetainRequest{Bank: testBank, RunAsync: async, Items: []RetainItem{
			{DocumentID: "ok", Content: "fine"},
			{
				DocumentID: "bad", Content: "too many", Tags: []string{"a", "b", "c", "d"},
				ObservationScopeModes: &domain.ObservationScopeModes{
					Mode:            domain.ScopeModeAllCombinations,
					MaxCombinations: &limit,
				},
			},
		}})
		if !errors.Is(err, domain.ErrTooManyScopes) || !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("async=%v: err = %v, want ErrTooManyScopes (INVALID_ARGUMENT)", async, err)
		}
	}
	if keys := e.blob.Keys(); len(keys) != 0 {
		t.Errorf("blobs written: %v", keys)
	}
	if s, _ := e.store.Read().BankSeq(e.ctx, testBank); s != seq {
		t.Errorf("bank seq moved %d -> %d", seq, s)
	}
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		_, err := tx.Documents().Get(e.ctx, testBank, "ok")
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("document of the valid item exists: %v", err)
	}
	if len(e.llm.Calls()) != 0 || len(e.operations("")) != 0 {
		t.Errorf("calls %d, operations %d; want none", len(e.llm.Calls()), len(e.operations("")))
	}

	// Within the cap (3 tags = 7 combinations) the same item is accepted.
	e.ingest(RetainRequest{Items: []RetainItem{{
		DocumentID: "bad", Content: "ok now", Tags: []string{"a", "b", "c"},
		ObservationScopeModes: &domain.ObservationScopeModes{Mode: domain.ScopeModeAllCombinations, MaxCombinations: ptr(7)},
	}}})
}

// TestIngestDeletingBankStoresNoBlob: a retain to a bank being deleted fails with
// ErrBankDeleting (FAILED_PRECONDITION) before the blob Put, so no blob lands under the bank's prefix.
func TestIngestDeletingBankStoresNoBlob(t *testing.T) {
	e := newEnv(t, 0)
	e.inTx(func(store.Tx) error { return nil }) // creates the bank
	e.inTx(func(tx store.Tx) error { return tx.Banks().MarkDeleting(e.ctx, testBank, time.Now()) })
	for _, async := range []bool{false, true} {
		_, err := e.ingestor.Ingest(e.ctx, RetainRequest{Bank: testBank, RunAsync: async, Items: []RetainItem{
			{DocumentID: "d1", Content: "Alice likes tea."},
		}})
		if !errors.Is(err, domain.ErrBankDeleting) || !errors.Is(err, domain.ErrFailedPrecondition) {
			t.Fatalf("async=%v: err = %v, want ErrBankDeleting (FAILED_PRECONDITION)", async, err)
		}
	}
	if keys := e.blob.Keys(); len(keys) != 0 {
		t.Errorf("blobs written for a deleting bank: %v", keys)
	}
	if len(e.llm.Calls()) != 0 {
		t.Errorf("%d LLM calls, want none", len(e.llm.Calls()))
	}
}

// TestIngestAsyncEnqueuesChildrenUnderBatch: an async retain of several documents enqueues one
// retain_document job per changed document under a retain_batch parent, with the merged overrides on the job
// and the item fields in the payload; nothing is extracted until a worker runs the job.
func TestIngestAsyncEnqueuesChildrenUnderBatch(t *testing.T) {
	e := newEnv(t, 0)
	ts := e.clock.Now().Add(-48 * time.Hour)
	res := e.ingest(RetainRequest{
		RunAsync: true, DocumentTags: []string{"Team"}, Overrides: &domain.BankConfig{
			Retain: &domain.RetainConfig{Mission: "req"},
		},
		Items: []RetainItem{
			{
				DocumentID: "d1", Content: "one", Tags: []string{"x"}, Timestamp: &ts, Context: "chat",
				Entities:  []domain.EntityHint{{Text: "Alice", Type: "person"}},
				Overrides: &domain.BankConfig{Retain: &domain.RetainConfig{ExtractionMode: domain.ExtractionModeVerbose}},
			},
			{Content: "two", Timeless: true},
		},
	})
	if len(res.Documents) != 2 || res.Documents[1].DocumentID == "" {
		t.Fatalf("result = %+v", res)
	}
	checkOperations(t, res)
	if len(e.llm.Calls()) != 0 {
		t.Fatalf("async retain called the model")
	}
	children := e.operations(domain.OperationKindRetainDocument)
	if len(children) != 2 {
		t.Fatalf("%d retain_document jobs, want 2", len(children))
	}
	checkFirstChild(e, e.claimRetain(), res, ts)
}

// checkOperations checks an async retain returns the operations it enqueued, read back whole: a pending
// retain_batch parent of the bank, and one pending retain_document child per document under it.
func checkOperations(t *testing.T, res RetainResult) {
	t.Helper()
	p := res.BatchOperation
	if p == nil || p.ID == "" || p.Bank != testBank || p.Kind != domain.OperationKindRetainBatch ||
		p.Status != domain.OperationStatusPending {
		t.Fatalf("batch operation = %+v", p)
	}
	for i, d := range res.Documents {
		op := d.Operation
		if op == nil || op.Bank != testBank || op.Kind != domain.OperationKindRetainDocument || op.ParentID != p.ID ||
			op.Status != domain.OperationStatusPending || op.CreatedAt.IsZero() {
			t.Fatalf("document %d operation = %+v", i, op)
		}
	}
}

// checkFirstChild checks the first item's job: parent, serial key, merged overrides and pinned payload.
func checkFirstChild(e *env, j store.Job, res RetainResult, ts time.Time) {
	t := e.t
	t.Helper()
	if j.ParentID != res.BatchOperation.ID || j.SerialKey != jobs.DocumentSerialKey("d1") ||
		j.ID != res.Documents[0].Operation.ID {
		t.Errorf("job = %+v", j)
	}
	o := j.Overrides
	if o == nil || o.GetRetain().Mission != "req" || o.GetRetain().ExtractionMode != domain.ExtractionModeVerbose {
		t.Errorf("job overrides = %+v, want request < item merged", o)
	}
	checkPayload(e, j, ts)
	if d := e.document("d1"); d.ExtractedAt != nil || d.Overrides == nil {
		t.Errorf("document = %+v, want in flight with overrides stored", d)
	}
}

// checkPayload checks the first item's pinned payload.
func checkPayload(e *env, j store.Job, ts time.Time) {
	t := e.t
	t.Helper()
	var p jobs.RetainPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Context != "chat" || len(p.Tags) != 2 || p.Tags[0] != "team" || p.Timestamp == nil || !p.Timestamp.Equal(ts) {
		t.Errorf("payload = %+v", p)
	}
	if len(p.Entities) != 1 || p.BlobKey != blob.Key(testBank, "d1", contentHash("one")) {
		t.Errorf("payload hints / blob key = %+v", p)
	}
}

// TestIngestValidation: bad requests fail with INVALID_ARGUMENT before anything is stored.
func TestIngestValidation(t *testing.T) {
	e := newEnv(t, 0)
	custom := &domain.BankConfig{Retain: &domain.RetainConfig{ExtractionMode: domain.ExtractionModeCustom}}
	cases := map[string]RetainRequest{
		"no items": {Bank: testBank},
		"no bank":  {Items: []RetainItem{{Content: "x"}}},
		"duplicate doc": {Bank: testBank, Items: []RetainItem{
			{DocumentID: "a", Content: "x"}, {DocumentID: "a", Content: "y"},
		}},
		"custom no prompt": {Bank: testBank, Items: []RetainItem{{Content: "x", Overrides: custom}}},
	}
	for name, req := range cases {
		if _, err := e.ingestor.Ingest(e.ctx, req); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want INVALID_ARGUMENT", name, err)
		}
	}
	if len(e.blob.Keys()) != 0 {
		t.Error("blobs written")
	}
}
