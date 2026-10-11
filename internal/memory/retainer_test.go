package memory

import (
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"testing"
	"time"
)

// scriptChunks scripts one reply per chunk (fact "fact i") followed by failures, so call n+1 fails.
func (e *env) scriptChunks(okCalls int, fail bool) {
	e.t.Helper()
	e.llm.Reset()
	var replies []fake.Reply
	for i := 0; i < okCalls; i++ {
		replies = append(replies, reply(e.t, fact("fact "+string(rune('a'+i)))))
	}
	if fail {
		replies = append(replies, fake.Reply{Err: errExtract})
	}
	e.llm.Script(domain.ModelSlotExtract, prompts.ExtractConcise, replies...)
}

// asyncRetain enqueues one document and claims its job.
func (e *env) asyncRetain(doc domain.DocumentID, content string, force bool) store.Job {
	e.t.Helper()
	e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{{
		DocumentID: doc, Content: content, Overrides: chunkTokens(12), ForceReextract: force,
	}}})
	return e.claimRetain()
}

// TestRetainResumeAfterCrashReextractsOnlyRemainingChunks: a crash after chunk 3 of 6, then a retry of the
// same job, re-extracts only chunks 4-6.
func TestRetainResumeAfterCrashReextractsOnlyRemainingChunks(t *testing.T) {
	e := newEnv(t, 1)
	j := e.asyncRetain("d1", paragraphs(6, "one"), false)

	e.scriptChunks(3, true)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); !errors.Is(err, errExtract) {
		t.Fatalf("first run: err = %v, want the scripted crash", err)
	}
	if got := e.extractCalls(); got != 4 {
		t.Fatalf("first run made %d extract calls, want 4 (3 ok + the crash)", got)
	}
	if n := e.document("d1").MemoryCount; n != 3 {
		t.Fatalf("after crash: %d units, want 3", n)
	}

	e.scriptChunks(3, false)
	res, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := e.extractCalls(); got != 3 {
		t.Errorf("retry made %d extract calls, want 3 (chunks 4-6)", got)
	}
	if res.ChunksTotal != 6 || res.ChunksSkipped != 3 || res.MemoriesCreated != 3 {
		t.Errorf("retry result = total %d skipped %d created %d, want 6 / 3 / 3",
			res.ChunksTotal, res.ChunksSkipped, res.MemoriesCreated)
	}
	d := e.document("d1")
	if d.MemoryCount != 6 || d.ExtractedAt == nil {
		t.Errorf("document: %d units, extracted_at %v; want 6 units and extracted_at set", d.MemoryCount, d.ExtractedAt)
	}
}

// TestRetainChangedDocumentCrashResumeKeepsAllFacts is the data-loss trap: Chunks.Committed still holds the
// previous retain's hashes. A re-retain of a changed document (same text in chunks 1-5) that crashes after
// the first batch must, on resume, re-extract every chunk it did not commit itself, even those whose text and
// index match a leftover chunk of the old retain (whose units the first batch already deleted).
func TestRetainChangedDocumentCrashResumeKeepsAllFacts(t *testing.T) {
	e := newEnv(t, 3)
	v1 := paragraphs(6, "one")
	e.scriptChunks(6, false)
	e.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d1", Content: v1, Overrides: chunkTokens(12)}}})
	if n := e.document("d1").MemoryCount; n != 6 {
		t.Fatalf("v1: %d units, want 6", n)
	}

	v2 := strings.Replace(v1, "Paragraph A talks about topic one", "Paragraph A talks about topic two", 1)
	j := e.asyncRetain("d1", v2, false)
	e.scriptChunks(3, true) // batch 1 (chunks 0-2) commits, batch 2 crashes on chunk 3
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); !errors.Is(err, errExtract) {
		t.Fatalf("first run: err = %v, want the scripted crash", err)
	}
	if n := e.document("d1").MemoryCount; n != 3 {
		t.Fatalf("after crash: %d units, want 3 (old units replaced, batch 1 committed)", n)
	}

	e.scriptChunks(3, false)
	res, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := e.extractCalls(); got != 3 {
		t.Errorf("resume made %d extract calls, want 3 (chunks 3-5 re-extracted)", got)
	}
	if res.ChunksSkipped != 3 {
		t.Errorf("resume skipped %d chunks, want 3", res.ChunksSkipped)
	}
	if n := e.document("d1").MemoryCount; n != 6 {
		t.Errorf("after resume: %d units, want 6 (no fact lost)", n)
	}
}

// TestRetainForceReextractCrashResume: force_reextract of unchanged content replaces the units, and a crash
// midway resumes without skipping the chunks the old retain committed with the same text.
func TestRetainForceReextractCrashResume(t *testing.T) {
	e := newEnv(t, 1)
	content := paragraphs(6, "one")
	e.scriptChunks(6, false)
	e.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d1", Content: content, Overrides: chunkTokens(12)}}})

	j := e.asyncRetain("d1", content, true)
	e.scriptChunks(3, true)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); !errors.Is(err, errExtract) {
		t.Fatalf("first run: err = %v, want the scripted crash", err)
	}
	e.scriptChunks(3, false)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := e.extractCalls(); got != 3 {
		t.Errorf("resume made %d extract calls, want 3", got)
	}
	if n := e.document("d1").MemoryCount; n != 6 {
		t.Errorf("after resume: %d units, want 6", n)
	}
}

// TestRetainCompletedJobReplayIsNoop: a job replayed after it completed (lost ack) changes nothing.
func TestRetainCompletedJobReplayIsNoop(t *testing.T) {
	e := newEnv(t, 0)
	j := e.asyncRetain("d1", "Alice likes tea.", false)
	e.scriptChunks(1, false)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); err != nil {
		t.Fatal(err)
	}
	res, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil || !res.Unchanged {
		t.Fatalf("replay = %+v, %v; want unchanged", res, err)
	}
	if got := e.extractCalls(); got != 1 {
		t.Errorf("%d extract calls, want 1", got)
	}
	if n := e.document("d1").MemoryCount; n != 1 {
		t.Errorf("%d units, want 1", n)
	}
}

// TestRetainReplacementDeletesObservationsAndResetsCoSources: replacing a document deletes the observations
// citing its old units and resets consolidated_at on their other sources, and queues graph maintenance.
func TestRetainReplacementDeletesObservationsAndResetsCoSources(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	a := e.ingest(RetainRequest{SkipConsolidation: true, Items: []RetainItem{{DocumentID: "a", Content: "Alice tea"}}})
	b := e.ingest(RetainRequest{SkipConsolidation: true, Items: []RetainItem{{DocumentID: "b", Content: "Bob tea"}}})
	if a.Documents[0].MemoriesCreated != 1 || b.Documents[0].MemoriesCreated != 1 {
		t.Fatalf("setup: %+v %+v", a, b)
	}
	aUnit, bUnit := e.docUnit("a"), e.docUnit("b")
	obs1 := pgtest.ID[domain.UnitID]("obs1")
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		obs := domain.Unit{ID: obs1, Bank: testBank, Type: domain.FactTypeObservation, Text: "people like tea", Seq: seq}
		if err := tx.Units().Insert(e.ctx, []domain.Unit{obs}); err != nil {
			return err
		}
		if err := tx.Evidence().Add(e.ctx, obs1, []domain.Evidence{{Unit: aUnit}, {Unit: bUnit}}); err != nil {
			return err
		}
		return tx.Units().MarkConsolidated(e.ctx, []domain.UnitID{aUnit, bUnit}, e.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}

	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes coffee")))
	e.ingest(RetainRequest{SkipConsolidation: true, Items: []RetainItem{{DocumentID: "a", Content: "Alice coffee"}}})

	if got := e.units([]domain.UnitID{obs1, aUnit}); len(got) != 0 {
		t.Errorf("observation or old unit still live: %+v", got)
	}
	got := e.units([]domain.UnitID{bUnit})
	if len(got) != 1 || got[0].ConsolidatedAt != nil {
		t.Errorf("co-source b = %+v, want live with consolidated_at reset", got)
	}
	if n := e.document("a").MemoryCount; n != 1 {
		t.Errorf("document a has %d units, want 1 (the replacement)", n)
	}
	if ops := e.operations(domain.OperationKindGraphMaintenance); len(ops) != 1 {
		t.Errorf("%d graph_maintenance jobs, want 1", len(ops))
	}
}

// docUnit returns the one live unit of a document, found through its retain's committed chunk.
func (e *env) docUnit(doc domain.DocumentID) domain.UnitID {
	e.t.Helper()
	hits, err := e.store.Read().SearchText(e.ctx, store.TextQuery{Bank: testBank, Text: "likes", Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, h := range hits {
		if us := e.units([]domain.UnitID{h.ID}); len(us) == 1 && us[0].DocumentID == doc {
			return h.ID
		}
	}
	e.t.Fatalf("no unit for document %s", doc)
	return ""
}

// TestRetainTimelessAndTimestamp: a timeless item stores no dates; an explicit timestamp is the mention time
// and anchors relative dates; no timestamp means now.
func TestRetainTimelessAndTimestamp(t *testing.T) {
	e := newEnv(t, 0)
	ts := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)
	event := fact("Alice ran a marathon")
	event.FactKind, event.TimeExpression = "event", ptr("yesterday")
	dated := fact("Bob moved to Paris")
	dated.FactKind, dated.OccurredStart = "event", ptr("2020-05-01T00:00:00Z")

	tests := []struct {
		name      string
		item      RetainItem
		facts     []jsonFact
		mentioned *time.Time
		start     *time.Time
	}{
		{"timeless drops every date", RetainItem{Timeless: true, Timestamp: &ts}, []jsonFact{dated}, nil, nil},
		{
			"timestamp anchors relative dates",
			RetainItem{Timestamp: &ts},
			[]jsonFact{event},
			&ts,
			ptr(time.Date(2024, 3, 14, 0, 0, 0, 0, time.UTC)),
		},
		{
			"absolute dates kept",
			RetainItem{Timestamp: &ts},
			[]jsonFact{dated},
			&ts,
			ptr(time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)),
		},
		{"no timestamp is now", RetainItem{}, []jsonFact{fact("plain")}, ptr(e.clock.Now().UTC()), nil},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e.llm.Reset()
			e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, tc.facts...))
			tc.item.DocumentID, tc.item.Content = domain.DocumentID(fmt.Sprintf("t%d", i)), "content "+tc.name
			e.ingest(RetainRequest{Items: []RetainItem{tc.item}})
			u := e.units([]domain.UnitID{e.onlyUnit(tc.item.DocumentID)})[0]
			if u.Timeless != tc.item.Timeless || !sameTime(u.MentionedAt, tc.mentioned) || !sameTime(u.OccurredStart, tc.start) {
				t.Errorf("unit timeless %v mentioned %v start %v; want %v %v %v", u.Timeless, u.MentionedAt,
					u.OccurredStart, tc.item.Timeless, tc.mentioned, tc.start)
			}
			if tc.item.Timeless && u.OccurredEnd != nil {
				t.Errorf("timeless unit has occurred_end %v", u.OccurredEnd)
			}
		})
	}
}

// onlyUnit returns the single unit of a document via the vector arm (every unit has an embedding).
func (e *env) onlyUnit(doc domain.DocumentID) domain.UnitID {
	e.t.Helper()
	hits, err := e.store.Read().SearchVector(e.ctx, store.VectorQuery{
		Bank: testBank, Embedding: e.emb.Vector("x"), MinSimilarity: -2, Limit: 1000,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	var ids []domain.UnitID
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	var found []domain.UnitID
	for _, u := range e.units(ids) {
		if u.DocumentID == doc {
			found = append(found, u.ID)
		}
	}
	if len(found) != 1 {
		e.t.Fatalf("document %s has units %v, want exactly one", doc, found)
	}
	return found[0]
}

// sameTime compares optional times.
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// TestRetainStampsSeqAndEnqueuesConsolidate: units and the RETAIN_COMPLETED event carry the transaction's
// seq; consolidation is queued once (deduplicated) and not at all with skip_consolidation or observations off.
func TestRetainStampsSeqAndEnqueuesConsolidate(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("Alice likes tea")))
	res := e.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d1", Content: "Alice likes tea"}}})
	if res.Documents[0].MemoriesCreated != 1 || len(res.Usage) != 1 || res.Usage[0].Slot != domain.ModelSlotExtract {
		t.Fatalf("result = %+v", res)
	}
	u := e.units([]domain.UnitID{e.onlyUnit("d1")})[0]
	var evs []domain.LifecycleEvent
	if err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		var err error
		evs, _, err = tx.Events().List(e.ctx, testBank, 0, 100, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Type != domain.EventTypeRetainCompleted || evs[0].SubjectID != "d1" {
		t.Fatalf("events = %+v", evs)
	}
	if u.Seq <= 0 || evs[0].Seq <= u.Seq {
		t.Errorf("unit seq %d, event seq %d; want both set, the completion after the batch", u.Seq, evs[0].Seq)
	}
	e.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d2", Content: "Alice likes green tea"}}})
	if ops := e.operations(domain.OperationKindConsolidate); len(ops) != 1 {
		t.Errorf("%d consolidate jobs, want 1 (deduplicated)", len(ops))
	}

	e2 := newEnv(t, 0)
	e2.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	e2.ingest(RetainRequest{SkipConsolidation: true, Items: []RetainItem{{DocumentID: "d1", Content: "a"}}})
	if ops := e2.operations(domain.OperationKindConsolidate); len(ops) != 0 {
		t.Errorf("%d consolidate jobs with skip_consolidation, want 0", len(ops))
	}
	off := false
	if err := e2.store.InTx(e2.ctx, func(tx store.Tx) error {
		return tx.Banks().SetConfig(e2.ctx, testBank, domain.BankConfig{
			Observations: &domain.ObservationConfig{Enabled: &off},
		})
	}); err != nil {
		t.Fatal(err)
	}
	e2.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d2", Content: "b"}}})
	if ops := e2.operations(domain.OperationKindConsolidate); len(ops) != 0 {
		t.Errorf("%d consolidate jobs with observations disabled, want 0", len(ops))
	}
}
