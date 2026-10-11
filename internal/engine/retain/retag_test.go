package retain

import (
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/port"
	"slices"
	"testing"
)

// docUnits lists the live units of a document.
func (e *env) docUnits(doc port.DocumentID) []domain.Unit {
	e.t.Helper()
	var us []domain.Unit
	e.inTx(func(tx port.Tx) error {
		var err error
		us, err = tx.ListUnits(e.ctx, port.UnitQuery{
			Bank: testBank, DocumentID: doc, State: domain.MemoryStateValid, Limit: 1000,
		})
		return wrap(err)
	})
	return us
}

// retag replaces d1's tags through UpdateDocument's Retagger and fails the test unless a retag_document
// operation was enqueued.
func (e *env) retag(tags ...string) {
	e.t.Helper()
	id, err := docs.NewRetagger(e.store).Retag(e.ctx, testBank, "d1", tags)
	if err != nil || id == "" {
		e.t.Fatalf("Retag(%v) = %q, %v; want an enqueued retag_document", tags, id, err)
	}
}

// applyRetag completes retain job j, then claims and runs the retag_document job queued behind it on the
// same "doc:" serial key.
func (e *env) applyRetag(j port.Job) {
	e.t.Helper()
	if err := e.store.Complete(e.ctx, j.ID, nil, nil); err != nil {
		e.t.Fatal(err)
	}
	jobs, err := e.store.Claim(e.ctx, "w1", []port.JobKind{domain.OperationKindRetagDocument}, 1)
	if err != nil || len(jobs) != 1 {
		e.t.Fatalf("Claim retag_document: %v, %d jobs", err, len(jobs))
	}
	if err := docs.NewRetagger(e.store).Apply(e.ctx, jobs[0]); err != nil {
		e.t.Fatalf("retag_document: %v", err)
	}
}

// checkRetagged fails the test unless d1 is complete with exactly the facts "fact a" .. "fact c", each
// carrying exactly tags.
func (e *env) checkRetagged(stage string, tags ...string) {
	e.t.Helper()
	d := e.document("d1")
	if d.ExtractedAt == nil || d.UnitCount != 3 || !domain.SameTagSet(d.Tags, tags) {
		e.t.Errorf("%s: document extracted_at %v, %d units, tags %v; want complete, 3 units, %v", stage,
			d.ExtractedAt, d.UnitCount, d.Tags, tags)
	}
	var texts []string
	for _, u := range e.docUnits("d1") {
		texts = append(texts, u.Text)
		if !domain.SameTagSet(u.Tags, tags) {
			e.t.Errorf("%s: unit %q tags %v, want %v", stage, u.Text, u.Tags, tags)
		}
	}
	slices.Sort(texts)
	if want := []string{"fact a", "fact b", "fact c"}; !slices.Equal(texts, want) {
		e.t.Errorf("%s: unit texts %v, want %v (every chunk's fact)", stage, texts, want)
	}
}

// runRetaggedBetweenBatches runs retain job j one chunk per batch, retagging d1 to [new] after the first
// batch committed, and checks that the run completes with every chunk's facts and that retag_document then
// leaves every unit tagged [new].
func (e *env) runRetaggedBetweenBatches(j port.Job) {
	e.t.Helper()
	e.scriptChunks(3, false)
	res, err := e.hookedRetainerBatch(1, 2, func() { e.retag("new") }).ProcessDocument(e.ctx, j)
	if err != nil || res.Superseded || res.UnitsCreated != 3 {
		e.t.Fatalf("retagged run = %+v, %v; want 3 units, not superseded", res, err)
	}
	var stale int
	for _, u := range e.docUnits("d1") {
		if !domain.SameTagSet(u.Tags, []string{"new"}) {
			stale++
		}
	}
	if stale != 0 { // UpdateDocument retags the first batch's units in its own tx (decision 1900)
		e.t.Errorf("%d units with stale tags before retag_document, want 0", stale)
	}
	e.applyRetag(j)
	e.checkRetagged("after retag_document", "new")
}

// TestReprocessRetaggedBetweenBatchesKeepsAllFacts (WP-17 accept a): a 3-chunk reprocess that a retag hits
// between batches adopts the new tags instead of stopping as superseded, so all 3 chunks' facts survive,
// extracted_at is set, and after retag_document every unit carries the new tags.
func TestReprocessRetaggedBetweenBatchesKeepsAllFacts(t *testing.T) {
	e := newEnv(t, 1)
	e.scriptChunks(3, false)
	e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	if _, err := NewReprocessor(e.store).Reprocess(e.ctx, testBank, "d1", nil); err != nil {
		t.Fatal(err)
	}
	e.runRetaggedBetweenBatches(e.claimRetain())
}

// TestRetainRetaggedBetweenBatchesKeepsAllFacts (WP-17 accept b): the same for a multi-batch async retain of
// new content.
func TestRetainRetaggedBetweenBatchesKeepsAllFacts(t *testing.T) {
	e := newEnv(t, 1)
	e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	e.runRetaggedBetweenBatches(e.claimRetain())
}

// TestReprocessRetaggedBeforeFirstBatchCompletes (WP-17 accept c): a retag after the reprocess was enqueued
// but before its first batch does not supersede it: the reprocess runs to completion and its units already
// carry the new tags.
func TestReprocessRetaggedBeforeFirstBatchCompletes(t *testing.T) {
	e := newEnv(t, 1)
	e.scriptChunks(3, false)
	e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	if _, err := NewReprocessor(e.store).Reprocess(e.ctx, testBank, "d1", nil); err != nil {
		t.Fatal(err)
	}
	e.retag("new")
	j := e.claimRetain()
	e.scriptChunks(3, false)
	res, err := e.retainer.ProcessDocument(e.ctx, j)
	if err != nil || res.Superseded || res.UnitsCreated != 3 {
		t.Fatalf("reprocess after retag = %+v, %v; want 3 units, not superseded", res, err)
	}
	e.checkRetagged("before retag_document", "new")
	e.applyRetag(j)
	e.checkRetagged("after retag_document", "new")
}

// TestRetainOverridesChangeMidRunSupersedes: a real ownership change still supersedes. A reprocess with new
// overrides lands between batches (same blob key, chunks untouched): the running job stops at its next
// transaction as superseded and never stamps the document complete.
func TestRetainOverridesChangeMidRunSupersedes(t *testing.T) {
	e := newEnv(t, 1)
	e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	j := e.claimRetain()
	e.scriptChunks(3, false)
	newOverrides := chunkTokens(12)
	newOverrides.RetainMission = "changed"
	overtake := func() {
		if _, err := NewReprocessor(e.store).Reprocess(e.ctx, testBank, "d1", newOverrides); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.hookedRetainerBatch(1, 2, overtake).ProcessDocument(e.ctx, j)
	if err != nil || !res.Superseded {
		t.Fatalf("run overtaken by new overrides = %+v, %v; want superseded", res, err)
	}
	if d := e.document("d1"); d.ExtractedAt != nil {
		t.Errorf("superseded run stamped the document complete: %+v", d)
	}
}
