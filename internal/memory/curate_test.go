package memory

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"testing"
)

// invalidate archives f1 through InvalidateMemory.
func (e *curateEnv) invalidate(s *Service) domain.Unit {
	e.t.Helper()
	u, op, err := s.InvalidateMemory(e.ctx, testBank, idF1, "wrong")
	if err != nil {
		e.t.Fatal(err)
	}
	if op != nil {
		e.t.Fatalf("invalidate returned operation %+v, want none", op)
	}
	return u
}

// assertNotLive fails for every id still live.
func (e *curateEnv) assertNotLive(ids ...domain.UnitID) {
	e.t.Helper()
	for _, id := range ids {
		if _, ok := e.unit(id); ok {
			e.t.Errorf("%s still live", id)
		}
	}
}

// searchIDs returns the ids the vector search (for text's embedding) and the text search (for q) return.
func (e *curateEnv) searchIDs(text, q string) (semantic, keyword []domain.UnitID) {
	e.t.Helper()
	vec, err := e.fake.Read().SearchVector(e.ctx, store.VectorQuery{
		Bank: testBank, Embedding: e.emb.Vector(text), Limit: 10,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	txt, err := e.fake.Read().SearchText(e.ctx, store.TextQuery{Bank: testBank, Text: q, Limit: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return hitIDs(vec), hitIDs(txt)
}

// serialKeys returns the sorted serial keys of the bank's operations of kind.
func (e *curateEnv) serialKeys(kind domain.OperationKind) []string {
	var keys []string
	for _, op := range e.ops(kind) {
		keys = append(keys, op.SerialKey)
	}
	slices.Sort(keys)
	return keys
}

// auditActions returns the recorded audit actions in order.
func (e *curateEnv) auditActions() []domain.CurationAction {
	var out []domain.CurationAction
	for _, a := range e.store.recorded() {
		out = append(out, a.Action)
	}
	return out
}

// assertInvalidateEffects checks the jobs, audit row and event an invalidate of f1 commits: a refresh for the
// model whose source_ids cite the fact (none for a model citing only the deleted observation or another
// fact), consolidate and graph.
func (e *curateEnv) assertInvalidateEffects() {
	e.t.Helper()
	t := e.t
	if keys := e.serialKeys(domain.OperationKindRefreshMentalModel); !slices.Equal(keys, []string{"mm:m-fact"}) {
		t.Errorf("refresh jobs = %v, want mm:m-fact", keys)
	}
	if keys := e.serialKeys(domain.OperationKindConsolidate); !slices.Equal(keys, []string{"consolidate"}) {
		t.Errorf("consolidate jobs = %v", keys)
	}
	if keys := e.serialKeys(domain.OperationKindGraphMaintenance); !slices.Equal(keys, []string{"graph"}) {
		t.Errorf("graph_maintenance jobs = %v", keys)
	}
	if audits := e.store.recorded(); len(audits) != 1 || audits[0].Action != domain.CurationInvalidate ||
		audits[0].Reason != "wrong" || audits[0].Before == nil {
		t.Errorf("audit rows = %+v", audits)
	}
	evs := e.events()
	if len(evs) != 1 || evs[0].Type != domain.EventTypeMemoryCurated || evs[0].SubjectID != string(idF1) {
		t.Errorf("events = %+v", evs)
	}
}

func TestInvalidateRemovesCitingObservationsAndQueuesRefresh(t *testing.T) {
	e := newCurateEnv(t)
	e.addModels(map[domain.MentalModelID][]domain.UnitID{
		"m-fact": {idF1, idO1}, "m-obs": {idO1}, "m-other": {idF2},
	})
	u := e.invalidate(e.service(nil))
	if u.ID != idF1 || u.State != domain.MemoryStateInvalidated || u.Embedding != nil {
		t.Fatalf("returned unit = %+v", u)
	}

	// Immediately (no job has run): neither the fact nor the observation citing it is visible to recall.
	e.assertNotLive(idF1, idO1)
	semantic, keyword := e.searchIDs("Alice likes green tea", "Alice")
	for _, id := range []domain.UnitID{idF1, idO1} {
		if slices.Contains(semantic, id) || slices.Contains(keyword, id) {
			t.Errorf("search still returns %s: semantic %v, keyword %v", id, semantic, keyword)
		}
	}
	if f2, ok := e.unit(idF2); !ok || f2.ConsolidatedAt != nil {
		t.Errorf("co-source f2 = %+v (live %v), want consolidated_at reset", f2.ConsolidatedAt, ok)
	}

	e.assertInvalidateEffects()
}

// restoreF1 invalidates f1, drains the consolidation it queued and restores it, returning the rederive job.
func (e *curateEnv) restoreF1(s *Service) store.Job {
	e.t.Helper()
	e.invalidate(s)
	e.drain(domain.OperationKindConsolidate)
	u, op, err := s.RestoreMemory(e.ctx, RestoreRequest{Bank: testBank, ID: idF1})
	if err != nil {
		e.t.Fatal(err)
	}
	if op == nil || u.State != domain.MemoryStateValid {
		e.t.Fatalf("restore = %+v, op %v", u, op)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	if len(jobs) != 1 || jobs[0].ID != op.ID || jobs[0].SerialKey != "doc:d1" {
		e.t.Fatalf("rederive jobs = %+v", jobs)
	}
	return jobs[0]
}

// assertCausalF1F2 checks the causal edge f1 -> f2 exists.
func (e *curateEnv) assertCausalF1F2() {
	e.t.Helper()
	hits, err := e.fake.Graph().Expand(e.ctx, graph.Query{
		Bank: testBank, Seeds: []domain.UnitID{idF1}, MaxHops: 1, Kinds: []graph.Kind{graph.KindCausal},
		Limit: 10,
	})
	if err != nil || !slices.Contains(hitIDs(hits), idF2) {
		e.t.Errorf("causal link f1->f2 not reattached: %v %v", hitIDs(hits), err)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	e := newCurateEnv(t)
	before, _ := e.unit(idF1)
	job := e.restoreF1(e.service(nil))

	got, ok := e.unit(idF1)
	if !ok || got.Seq <= before.Seq || got.Text != before.Text || got.Embedding != nil {
		t.Fatalf("restored unit = %+v (live %v)", got, ok)
	}
	if !slices.Equal(got.Entities, []string{"Alice"}) {
		t.Errorf("restored entities = %v, want [Alice]", got.Entities)
	}
	e.assertCausalF1F2()

	r := e.rederiver(nil)
	for range 2 { // a rerun after a reclaim is harmless
		if err := r.Rederive(e.ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if semantic, _ := e.searchIDs("Alice likes green tea", "Alice"); !slices.Contains(semantic, idF1) {
		t.Errorf("restored fact not searchable after rederive: %v", semantic)
	}
	if got, _ = e.unit(idF1); !slices.Equal(got.Entities, []string{"Alice"}) {
		t.Errorf("entities after rederive = %v, want [Alice]", got.Entities)
	}
	hits, err := e.fake.Graph().Expand(e.ctx, graph.Query{
		Bank: testBank, Seeds: []domain.UnitID{idF1}, MaxHops: 1, Kinds: []graph.Kind{graph.KindTemporal},
		Limit: 10,
	})
	if err != nil || !slices.Contains(hitIDs(hits), idF2) {
		t.Errorf("rederive did not relink f1 to f2 by time: %v %v", hitIDs(hits), err)
	}
	if n := len(e.claim(domain.OperationKindConsolidate)); n != 1 {
		t.Errorf("consolidate jobs after rederive = %d, want 1", n)
	}
	want := []domain.CurationAction{domain.CurationInvalidate, domain.CurationRestore}
	if got := e.auditActions(); !slices.Equal(got, want) {
		t.Errorf("audit actions = %v", got)
	}
}

// rederiveEdited checks the RPC already attached names, claims the one rederive job (its payload carries
// only the unit id) and runs it, checking the unit is embedded and still attached to names afterwards.
func (e *curateEnv) rederiveEdited(names []string) {
	e.t.Helper()
	t := e.t
	if got, _ := e.unit(idF1); !slices.Equal(got.Entities, names) {
		t.Fatalf("entities committed by the RPC = %v, want %v", got.Entities, names)
	}
	claimed := e.claim(domain.OperationKindRederiveMemory)
	if len(claimed) != 1 {
		t.Fatalf("rederive jobs = %d", len(claimed))
	}
	var p jobs.RederivePayload
	if err := json.Unmarshal(claimed[0].Payload, &p); err != nil || p.UnitID != idF1 {
		t.Fatalf("payload = %s (%v)", claimed[0].Payload, err)
	}
	if err := e.rederiver(nil).Rederive(e.ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.unit(idF1); got.Embedding == nil || !slices.Equal(got.Entities, names) {
		t.Errorf("after rederive: embedding set %v, entities %v", got.Embedding != nil, got.Entities)
	}
}

func TestEditClearsEmbeddingAndQueuesRederive(t *testing.T) {
	e := newCurateEnv(t)
	text := "Alice prefers oolong tea"
	u, op, err := e.curator().Update(e.ctx, UpdateRequest{
		Bank: testBank, ID: idF1, Text: &text, FactType: domain.FactTypeExperience,
		Entities: []string{"Alice", "Carol"}, Reason: "typo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op == nil || op.Kind != domain.OperationKindRederiveMemory ||
		u.Text != text || u.Type != domain.FactTypeExperience || u.EditedAt == nil {
		t.Fatalf("edit = %+v op %+v", u, op)
	}
	if got, _ := e.unit(idF1); got.Embedding != nil || got.Text != text || got.EditedAt == nil {
		t.Errorf("stored edit = %+v", got)
	}
	e.assertNotLive(idO1)
	if audits := e.store.recorded(); len(audits) != 1 || audits[0].Action != domain.CurationEdit ||
		audits[0].OperationID != op.ID || audits[0].Before == nil || audits[0].After == nil {
		t.Errorf("audit = %+v", audits)
	}
	e.rederiveEdited([]string{"Alice", "Carol"})
}

func TestEditClearEntities(t *testing.T) {
	e := newCurateEnv(t)
	_, _, err := e.curator().Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Entities: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	if len(jobs) != 1 {
		t.Fatalf("rederive jobs = %d", len(jobs))
	}
	if err := e.rederiver(nil).Rederive(e.ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.unit(idF1); len(got.Entities) != 0 {
		t.Errorf("entities = %v, want cleared", got.Entities)
	}
}

func TestCuratorRejectsObservations(t *testing.T) {
	e := newCurateEnv(t)
	c := e.curator()
	text := "x"
	empty := ""
	cases := []struct {
		name string
		req  UpdateRequest
		want error
	}{
		{"edit observation", UpdateRequest{ID: idO1, Text: &text}, domain.ErrInvalidArgument},
		{
			"invalidate observation",
			UpdateRequest{ID: idO1, State: domain.MemoryStateInvalidated},
			domain.ErrInvalidArgument,
		},
		{
			"invalidate with edits",
			UpdateRequest{ID: idF1, Text: &text, State: domain.MemoryStateInvalidated},
			domain.ErrInvalidArgument,
		},
		{"nothing to do", UpdateRequest{ID: idF1}, domain.ErrInvalidArgument},
		{"empty text", UpdateRequest{ID: idF1, Text: &empty}, domain.ErrInvalidArgument},
		{
			"to observation",
			UpdateRequest{ID: idF1, FactType: domain.FactTypeObservation},
			domain.ErrInvalidArgument,
		},
		{"unknown unit", UpdateRequest{ID: idNope, Text: &text}, domain.ErrNotFound},
		{"restore unknown", UpdateRequest{ID: idNope, State: domain.MemoryStateValid}, domain.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Bank = testBank
			if _, _, err := c.Update(e.ctx, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, ok := e.unit(idO1); !ok {
		t.Error("a rejected curation changed the observation")
	}
	if n := len(e.ops(domain.OperationKindRederiveMemory)); n != 0 {
		t.Errorf("rejected curations enqueued %d rederives", n)
	}
}

func TestRestoreLiveUnitIsNoop(t *testing.T) {
	e := newCurateEnv(t)
	u, op, err := e.curator().Update(e.ctx, UpdateRequest{
		Bank: testBank, ID: idF1, State: domain.MemoryStateValid,
	})
	if err != nil || op != nil || u.ID != idF1 {
		t.Fatalf("restore of a live unit = %+v %v %v", u, op, err)
	}
	if len(e.store.recorded()) != 0 {
		t.Error("no-op restore wrote an audit row")
	}
}

func TestRederiveSkipsWhenUnitGone(t *testing.T) {
	e := newCurateEnv(t)
	e.invalidate(e.service(nil))
	e.drain(domain.OperationKindConsolidate)
	job := store.Job{
		Bank: testBank, Kind: domain.OperationKindRederiveMemory,
		Payload: json.RawMessage(`{"unit_id":"` + string(idF1) + `"}`),
	}
	if err := e.rederiver(nil).Rederive(e.ctx, job); err != nil {
		t.Fatal(err)
	}
	if n := len(e.claim(domain.OperationKindConsolidate)); n != 0 {
		t.Errorf("rederive of a gone unit enqueued %d consolidations", n)
	}
	bad := store.Job{Bank: testBank, Kind: domain.OperationKindRederiveMemory, Payload: json.RawMessage(`{}`)}
	if err := e.rederiver(nil).Rederive(e.ctx, bad); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("bad payload err = %v", err)
	}
}

// editingEmbedder edits the unit text while the rederive embeds, like a concurrent UpdateMemory.
type editingEmbedder struct {
	ai.Embedder
	edit func()
}

// EmbedDocuments runs the edit, then embeds.
func (e editingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	e.edit()
	return e.Embedder.EmbedDocuments(ctx, texts)
}

func TestRederiveSkipsWhenTextChanged(t *testing.T) {
	e := newCurateEnv(t)
	c := e.curator()
	first := "Alice likes jasmine tea"
	if _, _, err := c.Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Text: &first}); err != nil {
		t.Fatal(err)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	second := "Alice likes black tea"
	emb := editingEmbedder{Embedder: e.emb, edit: func() {
		if _, _, err := c.Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Text: &second}); err != nil {
			t.Error(err)
		}
	}}
	if err := e.rederiver(emb).Rederive(e.ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	got, _ := e.unit(idF1)
	if got.Embedding != nil || got.Text != second {
		t.Errorf("stale rederive wrote an embedding for %q (text now %q)", first, got.Text)
	}
}
