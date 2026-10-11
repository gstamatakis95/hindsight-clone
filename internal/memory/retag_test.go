package memory

import (
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
)

// The document feature owns RetagDocument and ReprocessDocument; memory never imports it. These tests commit
// what those operations commit, through the store and the jobs catalogue, to check how a running retain
// reacts to them.

// docUnits lists the live units of a document.
func (e *env) docUnits(doc domain.DocumentID) []domain.Unit {
	e.t.Helper()
	var us []domain.Unit
	e.inTx(func(tx store.Tx) error {
		var err error
		us, err = tx.ListUnits(e.ctx, store.UnitQuery{
			Bank: testBank, DocumentID: doc, State: domain.MemoryStateValid, Limit: 1000,
		})
		return wrap(err)
	})
	return us
}

// retag commits what RetagDocument commits for d1: under the bank's change seq, the document's new tags, its
// units' document-derived tags (old to new) and the retag_document job queued behind any retain on the same
// "doc:" serial key.
func (e *env) retag(tags ...string) {
	e.t.Helper()
	want := domain.NormalizeTags(tags)
	e.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		d, err := tx.Documents().Get(e.ctx, testBank, "d1")
		if err != nil {
			return err
		}
		if err := tx.Documents().SetTags(e.ctx, testBank, "d1", want); err != nil {
			return err
		}
		if _, err := tx.Units().SetDocumentTags(e.ctx, testBank, "d1", domain.NormalizeTags(d.Tags), want, seq); err != nil {
			return err
		}
		_, _, err = jobs.EnqueueRetag(e.ctx, tx, testBank, "d1")
		return err
	})
}

// reprocess commits what ReprocessDocument commits for d1: under the bank's change seq, the document
// re-upserted (with overrides replacing the stored ones when set) and a forced retain_document job over its
// stored text.
func (e *env) reprocess(overrides *domain.BankConfig) {
	e.t.Helper()
	e.inTx(func(tx store.Tx) error {
		if _, err := tx.Seq(e.ctx, testBank); err != nil {
			return err
		}
		d, err := tx.Documents().Get(e.ctx, testBank, "d1")
		if err != nil {
			return err
		}
		if overrides != nil {
			d.Overrides = overrides
		}
		if err := tx.Documents().Upsert(e.ctx, domain.Document{
			ID: d.ID, Bank: testBank, Overrides: d.Overrides, Tags: d.Tags, BlobKey: d.BlobKey,
			ObservationScopeModes: d.ObservationScopeModes,
		}); err != nil {
			return err
		}
		p := jobs.RetainPayload{
			DocumentID: d.ID, ContentHash: d.BlobKey[strings.LastIndex(d.BlobKey, "/")+1:], BlobKey: d.BlobKey,
			Tags: domain.NormalizeTags(d.Tags), ObservationScopeModes: d.ObservationScopeModes, ForceReextract: true,
		}
		_, _, err = jobs.EnqueueRetainDocument(e.ctx, tx, testBank, p, d.Overrides, "")
		return err
	})
}

// applyRetag completes retain job j, then claims the retag_document job queued behind it on the same "doc:"
// serial key and commits what it commits: the document's current tags re-asserted on its units.
func (e *env) applyRetag(j store.Job) {
	e.t.Helper()
	if err := e.store.Complete(e.ctx, j.ID, nil, nil); err != nil {
		e.t.Fatal(err)
	}
	claimed, err := e.store.Claim(e.ctx, "w1", []domain.OperationKind{domain.OperationKindRetagDocument}, 1)
	if err != nil || len(claimed) != 1 {
		e.t.Fatalf("Claim retag_document: %v, %d jobs", err, len(claimed))
	}
	e.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		d, err := tx.Documents().Get(e.ctx, testBank, "d1")
		if err != nil {
			return err
		}
		cur := domain.NormalizeTags(d.Tags)
		_, err = tx.Units().SetDocumentTags(e.ctx, testBank, "d1", cur, cur, seq)
		return err
	})
}

// checkRetagged fails the test unless d1 is complete with exactly the facts "fact a" .. "fact c", each
// carrying exactly tags.
func (e *env) checkRetagged(stage string, tags ...string) {
	e.t.Helper()
	d := e.document("d1")
	if d.ExtractedAt == nil || d.MemoryCount != 3 || !domain.SameTagSet(d.Tags, tags) {
		e.t.Errorf("%s: document extracted_at %v, %d units, tags %v; want complete, 3 units, %v", stage,
			d.ExtractedAt, d.MemoryCount, d.Tags, tags)
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
func (e *env) runRetaggedBetweenBatches(j store.Job) {
	e.t.Helper()
	e.scriptChunks(3, false)
	res, err := e.hookedRetainerBatch(1, 2, func() { e.retag("new") }).ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil || res.Superseded || res.MemoriesCreated != 3 {
		e.t.Fatalf("retagged run = %+v, %v; want 3 units, not superseded", res, err)
	}
	var stale int
	for _, u := range e.docUnits("d1") {
		if !domain.SameTagSet(u.Tags, []string{"new"}) {
			stale++
		}
	}
	if stale != 0 { // RetagDocument retags the first batch's units in its own tx (decision 1900)
		e.t.Errorf("%d units with stale tags before retag_document, want 0", stale)
	}
	e.applyRetag(j)
	e.checkRetagged("after retag_document", "new")
}

// TestReprocessRetaggedBetweenBatchesKeepsAllFacts: a 3-chunk reprocess that a retag hits
// between batches adopts the new tags instead of stopping as superseded, so all 3 chunks' facts survive,
// extracted_at is set, and after retag_document every unit carries the new tags.
func TestReprocessRetaggedBetweenBatchesKeepsAllFacts(t *testing.T) {
	e := newEnv(t, 1)
	e.scriptChunks(3, false)
	e.ingest(RetainRequest{Items: []RetainItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	e.reprocess(nil)
	e.runRetaggedBetweenBatches(e.claimRetain())
}

// TestRetainRetaggedBetweenBatchesKeepsAllFacts: the same for a multi-batch async retain of
// new content.
func TestRetainRetaggedBetweenBatchesKeepsAllFacts(t *testing.T) {
	e := newEnv(t, 1)
	e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	e.runRetaggedBetweenBatches(e.claimRetain())
}

// TestReprocessRetaggedBeforeFirstBatchCompletes: a retag after the reprocess was enqueued
// but before its first batch does not supersede it: the reprocess runs to completion and its units already
// carry the new tags.
func TestReprocessRetaggedBeforeFirstBatchCompletes(t *testing.T) {
	e := newEnv(t, 1)
	e.scriptChunks(3, false)
	e.ingest(RetainRequest{Items: []RetainItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	e.reprocess(nil)
	e.retag("new")
	j := e.claimRetain()
	e.scriptChunks(3, false)
	res, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil || res.Superseded || res.MemoriesCreated != 3 {
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
	e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{{
		DocumentID: "d1", Content: paragraphs(3, "one"), Tags: []string{"old"}, Overrides: chunkTokens(12),
	}}})
	j := e.claimRetain()
	e.scriptChunks(3, false)
	newOverrides := chunkTokens(12)
	retGrp := newOverrides.GetRetain()
	retGrp.Mission = "changed"
	newOverrides.Retain = &retGrp
	overtake := func() {
		e.reprocess(newOverrides)
	}
	res, err := e.hookedRetainerBatch(1, 2, overtake).ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil || !res.Superseded {
		t.Fatalf("run overtaken by new overrides = %+v, %v; want superseded", res, err)
	}
	if d := e.document("d1"); d.ExtractedAt != nil {
		t.Errorf("superseded run stamped the document complete: %+v", d)
	}
}
