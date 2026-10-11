package retain

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/prompts"
	"memd/internal/store"
	"testing"
	"time"
)

// TestIngestStoresItemScopes: an item's observation_scopes are stored on its document row (the store's
// DocumentRepo.Upsert and Get), a nil scopes argument stores nil, and changing only the scopes is not a no-op.
func TestIngestStoresItemScopes(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	all := &domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations}
	item := IngestItem{DocumentID: "d1", Content: "c", Tags: []string{"a", "b"}, ObservationScopes: all}
	e.ingest(IngestRequest{Items: []IngestItem{item}})
	if s := e.document("d1").ObservationScopes; s == nil || s.Mode != domain.ScopeModeAllCombinations {
		t.Fatalf("stored scopes = %+v, want all_combinations", s)
	}
	if res := e.ingest(IngestRequest{Items: []IngestItem{item}}); !res.Documents[0].Unchanged {
		t.Fatalf("same scopes: %+v, want unchanged", res)
	}
	item.ObservationScopes = &domain.ObservationScopes{Mode: domain.ScopeModePerTag}
	if res := e.ingest(IngestRequest{Items: []IngestItem{item}}); res.Documents[0].Unchanged {
		t.Fatalf("changed scopes: %+v, want re-retained", res)
	}
	if s := e.document("d1").ObservationScopes; s == nil || s.Mode != domain.ScopeModePerTag {
		t.Errorf("stored scopes after the change = %+v, want per_tag", s)
	}
	e.ingest(IngestRequest{Items: []IngestItem{{DocumentID: "d2", Content: "c2"}}})
	if s := e.document("d2").ObservationScopes; s != nil {
		t.Errorf("item without scopes stored %+v, want nil", s)
	}
}

// TestRetainAsyncScopesNoop: on the async path the Retainer stamps the same no-op hash as the Ingestor computes
// (both mix the scopes in only when set), so re-ingesting the same item is Unchanged and a scope-only change
// is not; an item without scopes keeps the plain overrides hash.
func TestRetainAsyncScopesNoop(t *testing.T) {
	for _, scopes := range []*domain.ObservationScopes{nil, {Mode: domain.ScopeModePerTag}} {
		e := newEnv(t, 0)
		e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
		item := IngestItem{DocumentID: "d1", Content: "c", Tags: []string{"a"}, ObservationScopes: scopes}
		e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{item}})
		if _, err := e.retainer.ProcessDocument(e.ctx, e.claimRetain()); err != nil {
			t.Fatal(err)
		}
		d := e.document("d1")
		if scopes == nil {
			if oh, _ := OverridesHash(nil); d.OverridesHash != oh {
				t.Errorf("no scopes: stamped hash %q, want the plain overrides hash %q", d.OverridesHash, oh)
			}
		}
		if res := e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{item}}); !res.Documents[0].Unchanged {
			t.Errorf("scopes %+v: re-ingest after the async run = %+v, want unchanged", scopes, res)
		}
		item.ObservationScopes = &domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations}
		if res := e.ingest(IngestRequest{RunAsync: true, Items: []IngestItem{item}}); res.Documents[0].Unchanged {
			t.Errorf("scopes %+v: scope-only change = %+v, want re-retained", scopes, res)
		}
	}
}

// reprocessJob claims the reprocess job and decodes its payload.
func (e *env) reprocessJob() (store.Job, DocumentPayload) {
	e.t.Helper()
	j := e.claimRetain()
	p, err := DecodePayload(j.Payload)
	if err != nil {
		e.t.Fatal(err)
	}
	return j, p
}

// reprocessAt is the mention time of the item TestReprocessCarriesScopesAndOverrides retains.
var reprocessAt = time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

// checkReprocessPayload fails the test unless the reprocess job carries the item's scopes, context,
// metadata, mention time and content hash, is forced, and replays the stored overrides.
func checkReprocessPayload(t *testing.T, j store.Job, p DocumentPayload) {
	t.Helper()
	if p.ObservationScopes == nil || p.ObservationScopes.Mode != domain.ScopeModePerTag || !p.ForceReextract {
		t.Errorf("payload scopes %+v, force %v; want per_tag and forced", p.ObservationScopes, p.ForceReextract)
	}
	if p.Context != "chat" || p.Metadata["k"] != "v" || p.Timestamp == nil || !p.Timestamp.Equal(reprocessAt) {
		t.Errorf("payload context %q, metadata %v, timestamp %v; want the item's", p.Context, p.Metadata, p.Timestamp)
	}
	if p.ContentHash != ContentHash("some text") {
		t.Errorf("payload content hash %q, want the stored text's", p.ContentHash)
	}
	if j.Overrides == nil || j.Overrides.RetainMission != "stored" {
		t.Errorf("job overrides = %+v, want the stored ones replayed", j.Overrides)
	}
}

// TestReprocessCarriesScopesAndOverrides: Reprocess keeps the document's item scopes (on the row and in the
// payload), replays the stored overrides unless new ones are given, takes the item's context, metadata and
// mention time from a live unit, and the forced job re-extracts the stored text.
func TestReprocessCarriesScopesAndOverrides(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: "some text", Tags: []string{"a"}, Context: "chat", Timestamp: &reprocessAt,
		ObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag},
		Metadata:          map[string]string{"k": "v"}, Overrides: &domain.BankConfig{RetainMission: "stored"},
	}}})
	rp := NewReprocessor(e.store)

	opID, err := rp.Reprocess(e.ctx, testBank, "d1", nil)
	if err != nil || opID == "" {
		t.Fatalf("Reprocess: %q, %v", opID, err)
	}
	if s := e.document("d1").ObservationScopes; s == nil || s.Mode != domain.ScopeModePerTag {
		t.Fatalf("document scopes after reprocess = %+v, want per_tag (Upsert must carry them)", s)
	}
	j, p := e.reprocessJob()
	if j.ID != opID {
		t.Fatalf("claimed %s, want the reprocess operation %s", j.ID, opID)
	}
	checkReprocessPayload(t, j, p)
	before := e.extractCalls()
	res, err := e.retainer.ProcessDocument(e.ctx, j)
	if err != nil || res.Unchanged || e.extractCalls() != before+1 {
		t.Fatalf("reprocess run = %+v, %v; %d extract calls, want 1 more", res, err, e.extractCalls()-before)
	}
	u := e.units([]domain.UnitID{e.onlyUnit("d1")})[0]
	if u.Context != "chat" || u.MentionedAt == nil || !u.MentionedAt.Equal(reprocessAt) {
		t.Errorf("re-extracted unit %+v lost the item's context or mention time", u)
	}
	if err := e.store.Complete(e.ctx, j.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	checkReprocessNewOverrides(t, e, rp)
}

// checkReprocessNewOverrides reprocesses d1 with new overrides: they replace the stored ones on the document
// and the job, and the item scopes stay.
func checkReprocessNewOverrides(t *testing.T, e *env, rp Reprocessor) {
	t.Helper()
	if _, err := rp.Reprocess(e.ctx, testBank, "d1", &domain.BankConfig{RetainMission: "new"}); err != nil {
		t.Fatal(err)
	}
	j, _ := e.reprocessJob()
	d := e.document("d1")
	if d.Overrides == nil || d.Overrides.RetainMission != "new" || j.Overrides.RetainMission != "new" ||
		d.ObservationScopes == nil {
		t.Errorf("new overrides: document %+v, job overrides %+v", d, j.Overrides)
	}
}

// TestReprocessUnknownDocumentAndDeletingBank: an unknown document is NotFound and a tombstoned bank is
// BankDeleting; neither enqueues anything.
func TestReprocessUnknownDocumentAndDeletingBank(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	e.ingest(IngestRequest{Items: []IngestItem{{DocumentID: "d1", Content: "c"}}})
	rp := NewReprocessor(e.store)
	if _, err := rp.Reprocess(e.ctx, testBank, "nope", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown document: %v, want NotFound", err)
	}
	e.inTx(func(tx store.Tx) error {
		return tx.Banks().MarkDeleting(e.ctx, testBank, e.clock.Now())
	})
	if _, err := rp.Reprocess(e.ctx, testBank, "d1", nil); !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("deleting bank: %v, want BankDeleting", err)
	}
	if ops := e.operations(domain.OperationKindRetainDocument); len(ops) != 0 {
		raw, _ := json.Marshal(ops)
		t.Errorf("reprocess enqueued %s", raw)
	}
}

// The retain Reprocessor is what docs.DocumentAdmin delegates ReprocessDocument to (decision 1303).
var _ docs.Reprocessor = NewReprocessor(nil)

// TestReprocessIgnoresCuratedUnit: the item's context, timeless flag and mention time come from a unit no
// curator edited, so a curated context or timeless flag on one unit never spreads to the re-extracted ones
// (reprocess resets curation).
func TestReprocessIgnoresCuratedUnit(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("one"), fact("two")))
	e.ingest(IngestRequest{Items: []IngestItem{{
		DocumentID: "d1", Content: "some text", Context: "chat", Timestamp: &reprocessAt,
	}}})
	e.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		us, err := tx.ListUnits(e.ctx, store.UnitQuery{Bank: testBank, DocumentID: "d1", Limit: 10})
		if err != nil || len(us) != 2 {
			return fmt.Errorf("units of d1: %d, %w", len(us), err)
		}
		edited := e.clock.Now()
		u := us[0] // the newest: Reprocess must skip it
		u.Context, u.Timeless, u.EditedAt, u.Seq = "curated", true, &edited, seq
		return tx.Units().Update(e.ctx, u)
	})
	if _, err := NewReprocessor(e.store).Reprocess(e.ctx, testBank, "d1", nil); err != nil {
		t.Fatal(err)
	}
	_, p := e.reprocessJob()
	if p.Context != "chat" || p.Timeless || p.Timestamp == nil || !p.Timestamp.Equal(reprocessAt) {
		t.Errorf("payload context %q, timeless %v, timestamp %v; want the uncurated item values",
			p.Context, p.Timeless, p.Timestamp)
	}
}
