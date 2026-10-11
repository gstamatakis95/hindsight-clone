package memory

import (
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"testing"
)

// TestIngestStoresItemScopes: an item's observation_scope_modes are stored on its document row (the store's
// DocumentRepo.Upsert and Get), a nil scopes argument stores nil, and changing only the scopes is not a no-op.
func TestIngestStoresItemScopes(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	all := &domain.ObservationScopeModes{Mode: domain.ScopeModeAllCombinations}
	item := RetainItem{DocumentID: "d1", Content: "c", Tags: []string{"a", "b"}, ObservationScopeModes: all}
	e.ingest(RetainRequest{Items: []RetainItem{item}})
	if s := e.document("d1").ObservationScopeModes; s == nil || s.Mode != domain.ScopeModeAllCombinations {
		t.Fatalf("stored scopes = %+v, want all_combinations", s)
	}
	if res := e.ingest(RetainRequest{Items: []RetainItem{item}}); !res.Documents[0].Unchanged {
		t.Fatalf("same scopes: %+v, want unchanged", res)
	}
	item.ObservationScopeModes = &domain.ObservationScopeModes{Mode: domain.ScopeModePerTag}
	if res := e.ingest(RetainRequest{Items: []RetainItem{item}}); res.Documents[0].Unchanged {
		t.Fatalf("changed scopes: %+v, want re-retained", res)
	}
	if s := e.document("d1").ObservationScopeModes; s == nil || s.Mode != domain.ScopeModePerTag {
		t.Errorf("stored scopes after the change = %+v, want per_tag", s)
	}
	e.ingest(RetainRequest{Items: []RetainItem{{DocumentID: "d2", Content: "c2"}}})
	if s := e.document("d2").ObservationScopeModes; s != nil {
		t.Errorf("item without scopes stored %+v, want nil", s)
	}
}

// TestRetainAsyncScopesNoop: on the async path the Retainer stamps the same no-op hash as the Ingestor computes
// (both mix the scopes in only when set), so re-ingesting the same item is Unchanged and a scope-only change
// is not; an item without scopes keeps the plain overrides hash.
func TestRetainAsyncScopesNoop(t *testing.T) {
	for _, scopes := range []*domain.ObservationScopeModes{nil, {Mode: domain.ScopeModePerTag}} {
		e := newEnv(t, 0)
		e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
		item := RetainItem{DocumentID: "d1", Content: "c", Tags: []string{"a"}, ObservationScopeModes: scopes}
		e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{item}})
		if _, err := e.retainer.ProcessDocument(e.ctx, jobs.Inline(e.claimRetain())); err != nil {
			t.Fatal(err)
		}
		d := e.document("d1")
		if scopes == nil {
			if oh, _ := overridesHash(nil); d.OverridesHash != oh {
				t.Errorf("no scopes: stamped hash %q, want the plain overrides hash %q", d.OverridesHash, oh)
			}
		}
		if res := e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{item}}); !res.Documents[0].Unchanged {
			t.Errorf("scopes %+v: re-ingest after the async run = %+v, want unchanged", scopes, res)
		}
		item.ObservationScopeModes = &domain.ObservationScopeModes{Mode: domain.ScopeModeAllCombinations}
		if res := e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{item}}); res.Documents[0].Unchanged {
			t.Errorf("scopes %+v: scope-only change = %+v, want re-retained", scopes, res)
		}
	}
}
