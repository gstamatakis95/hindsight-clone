package retain

import (
	"context"
	"errors"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/routing"
	"memd/internal/port"
	"memd/internal/prompts"
	"testing"
)

// TestRetainSupersededAfterInterveningRetain: a job that crashed, then lost the document to a newer retain
// of other content, stops as superseded on resume instead of mixing its chunks with the newer content.
func TestRetainSupersededAfterInterveningRetain(t *testing.T) {
	e := newEnv(t, 1)
	j := e.asyncRetain("d1", paragraphs(6, "one"), false)
	e.scriptChunks(3, true)
	if _, err := e.retainer.ProcessDocument(e.ctx, j); !errors.Is(err, errExtract) {
		t.Fatalf("first run: %v", err)
	}
	e.scriptChunks(1, false)
	e.ingest(IngestRequest{Items: []IngestItem{{DocumentID: "d1", Content: "Short B.", Overrides: chunkTokens(12)}}})

	e.scriptChunks(6, false)
	res, err := e.retainer.ProcessDocument(e.ctx, j)
	if err != nil || !res.Superseded {
		t.Fatalf("resume = %+v, %v; want superseded", res, err)
	}
	d := e.document("d1")
	if e.extractCalls() != 0 || d.UnitCount != 1 || d.ContentHash != ContentHash("Short B.") || d.ExtractedAt == nil {
		t.Errorf("calls %d, document %+v; want the newer retain's single unit, untouched", e.extractCalls(), d)
	}
}

// TestRetainRetryOperationAfterNewerRetain is the reviewer's probe: J1 fails for good, J2 (other content)
// completes, RetryOperation(J1) runs again. J1 is superseded; the document stays J2's, and a later re-retain
// of J1's content is not wrongly reported unchanged.
func TestRetainRetryOperationAfterNewerRetain(t *testing.T) {
	e := newEnv(t, 1)
	j1 := e.asyncRetain("d1", paragraphs(6, "one"), false)
	e.scriptChunks(3, true)
	_, err := e.retainer.ProcessDocument(e.ctx, j1)
	if err := e.store.Fail(e.ctx, j1.ID, err, nil); err != nil {
		t.Fatal(err)
	}
	j2 := e.asyncRetain("d1", "Short B.", false)
	e.scriptChunks(1, false)
	if _, err := e.retainer.ProcessDocument(e.ctx, j2); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Complete(e.ctx, j2.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	e.inTx(func(tx port.Tx) error {
		_, err := tx.Queue().Retry(e.ctx, testBank, j1.ID)
		return wrap(err)
	})
	j1b := e.claimRetain()
	e.scriptChunks(6, false)
	res, err := e.retainer.ProcessDocument(e.ctx, j1b)
	if err != nil || !res.Superseded {
		t.Fatalf("retried J1 = %+v, %v; want superseded", res, err)
	}
	if n := e.document("d1").UnitCount; n != 1 {
		t.Errorf("%d units, want J2's 1", n)
	}
	r := e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: paragraphs(6, "one"),
		Overrides: chunkTokens(12),
	}}})
	if r.Documents[0].Unchanged || r.Documents[0].UnitsCreated != 6 || e.document("d1").UnitCount != 6 {
		t.Errorf("re-retain of J1's content = %+v, units %d; want 6 new units", r.Documents[0],
			e.document("d1").UnitCount)
	}
}

// hookExtractor runs hook before its n-th call (from 1), then delegates.
type hookExtractor struct {
	inner Extractor
	calls int
	at    int
	hook  func()
}

// Extract implements Extractor.
func (h *hookExtractor) Extract(ctx context.Context, in ExtractInput) ([]ExtractedFact, []domain.Usage, error) {
	h.calls++
	if h.calls == h.at {
		h.hook()
	}
	facts, usage, err := h.inner.Extract(ctx, in)
	return facts, usage, wrap(err)
}

// hookedRetainer is e's retainer with an extractor that runs hook before its at-th call.
func (e *env) hookedRetainer(at int, hook func()) Retainer {
	router := routing.NewRouter()
	return NewRetainer(RetainerDeps{
		Store: e.store, Blob: e.blob, Config: e.cfg,
		Extractor: &hookExtractor{inner: NewExtractor(e.llm, router), at: at, hook: hook}, Embedder: e.emb,
		Entities: NewEntityResolver(ResolverConfig{}), Labels: NewLabelResolver(), Linker: NewLinker(),
		Clock: e.clock, BatchChunks: 3,
	})
}

// TestRetainOverlappingRetainsOfOneDocument: a retain that is overtaken mid-run by another retain of the same
// document (same content with force_reextract, or other content) stops at its next transaction without
// writing; the newer retain's units are the document's. A sync retain overtaken this way fails ABORTED.
func TestRetainOverlappingRetainsOfOneDocument(t *testing.T) {
	content := paragraphs(6, "one")
	for name, other := range map[string]string{"same content forced": content, "other content": "Short B."} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 0)
			e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("a fact")))
			j := e.asyncRetain("d1", content, false)
			overtake := func() {
				e.ingest(IngestRequest{Items: []IngestItem{{
					DocumentID: "d1", Content: other, ForceReextract: true,
					Overrides: chunkTokens(12),
				}}})
			}
			res, err := e.hookedRetainer(4, overtake).ProcessDocument(e.ctx, j)
			if err != nil || !res.Superseded {
				t.Fatalf("overtaken run = %+v, %v; want superseded", res, err)
			}
			want := len(ChunkWith(other, ChunkOptions{MaxTokens: 12}, nil))
			d := e.document("d1")
			if d.UnitCount != int64(want) || d.ContentHash != ContentHash(other) || d.ExtractedAt == nil {
				t.Errorf("document %+v, want the newer retain's %d units", d, want)
			}
		})
	}
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("a fact")))
	overtake := func() {
		e.ingest(IngestRequest{Items: []IngestItem{{DocumentID: "d1", Content: "Short B.", Overrides: chunkTokens(12)}}})
	}
	ing := NewIngestor(IngestorDeps{
		Store: e.store, Blob: e.blob, Config: e.cfg,
		Retainer: e.hookedRetainer(4, overtake), Merge: config.Merge, Clock: e.clock,
	})
	_, err := ing.Ingest(e.ctx, IngestRequest{Bank: testBank, Items: []IngestItem{{
		DocumentID: "d1",
		Content:    content, Overrides: chunkTokens(12),
	}}})
	if !errors.Is(err, ErrSuperseded) || !errors.Is(err, domain.ErrConflict) {
		t.Errorf("overtaken sync retain: %v, want ErrSuperseded (ABORTED)", err)
	}
}

// TestIngestFanOutRejectionUnknownBankCreatesNothing: validation runs before the bank is created.
func TestIngestFanOutRejectionUnknownBankCreatesNothing(t *testing.T) {
	e := newEnv(t, 0)
	_, err := e.ingestor.Ingest(e.ctx, IngestRequest{Bank: testBank, Items: []IngestItem{{
		Content:           "x",
		Tags:              []string{"a", "b", "c", "d", "e"},
		ObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations},
	}}})
	if !errors.Is(err, domain.ErrTooManyScopes) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.store.Read().BankSeq(e.ctx, testBank); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bank exists after a rejected retain: %v", err)
	}
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	r := e.ingest(IngestRequest{Items: []IngestItem{{DocumentID: "d1", Content: "x"}}})
	if r.Documents[0].UnitsCreated != 1 {
		t.Errorf("first valid retain on a new bank: %+v", r)
	}
}
