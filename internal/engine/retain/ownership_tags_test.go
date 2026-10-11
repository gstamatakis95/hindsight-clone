package retain

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
)

// TestRetainOlderRunCannotOverwriteNewerTags: an older run of the same content (forced, so the no-op check
// does not stop it) that resumes after a newer retain changed only the document's tags still owns the
// document (same blob key and overrides), so it runs to completion. But it adopts the newer tags (decision
// 1700), so every unit carries the newer tags and none the older ones.
func TestRetainOlderRunCannotOverwriteNewerTags(t *testing.T) {
	e := newEnv(t, 1)
	content := paragraphs(3, "one")
	e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{{
		DocumentID: "d1", Content: content, Tags: []string{"old"}, ForceReextract: true, Overrides: chunkTokens(12),
	}}})
	j := e.claimRetain()
	e.scriptChunks(1, true)
	if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j)); !errors.Is(err, errExtract) {
		t.Fatalf("first run: %v", err)
	}
	e.scriptChunks(3, false)
	e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: content, Tags: []string{"new"}, Overrides: chunkTokens(12),
	}}})

	e.scriptChunks(3, false)
	res, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(j))
	if err != nil || res.Superseded {
		t.Fatalf("older run = %+v, %v; want it to complete under the adopted tags", res, err)
	}
	if d := e.document("d1"); d.UnitCount != 3 || d.ExtractedAt == nil || !domain.SameTagSet(d.Tags, []string{"new"}) {
		t.Errorf("document = %+v, want 3 units tagged new, complete", d)
	}
	hits, err := e.store.Read().SearchText(e.ctx, store.TextQuery{Bank: testBank, Text: "fact", Limit: 100})
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits %v, %v", hits, err)
	}
	for _, u := range e.units([]domain.UnitID{hits[0].ID, hits[1].ID, hits[2].ID}) {
		if !domain.SameTagSet(u.Tags, []string{"new"}) {
			t.Errorf("unit %s tags %v, want [new]", u.ID, u.Tags)
		}
	}
}
