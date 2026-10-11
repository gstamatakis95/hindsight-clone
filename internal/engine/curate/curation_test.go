package curate

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

// invalidate archives f1 through the curator.
func (e *env) invalidate(c Curator) domain.Unit {
	e.t.Helper()
	u, op, err := c.Update(e.ctx, UpdateMemoryRequest{
		Bank: testBank, ID: "f1", State: domain.MemoryStateInvalidated, Reason: "wrong",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if op != "" {
		e.t.Fatalf("invalidate returned operation %q, want none", op)
	}
	return u
}

// assertNotLive fails for every id still live.
func (e *env) assertNotLive(ids ...port.UnitID) {
	e.t.Helper()
	for _, id := range ids {
		if _, ok := e.unit(id); ok {
			e.t.Errorf("%s still live", id)
		}
	}
}

// searchIDs returns the ids the semantic arm (for text's embedding) and the keyword arm (for q) return.
func (e *env) searchIDs(text, q string) (semantic, keyword []port.UnitID) {
	e.t.Helper()
	vec, err := e.fake.Read().SearchVector(e.ctx, port.VectorQuery{
		Bank: testBank, Embedding: e.emb.Vector(text), Limit: 10,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	txt, err := e.fake.Read().SearchText(e.ctx, port.TextQuery{Bank: testBank, Text: q, Limit: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return hitIDs(vec), hitIDs(txt)
}

// serialKeys returns the sorted serial keys of the bank's operations of kind.
func (e *env) serialKeys(kind domain.OperationKind) []string {
	var keys []string
	for _, op := range e.ops(kind) {
		keys = append(keys, op.SerialKey)
	}
	slices.Sort(keys)
	return keys
}

// auditActions returns the recorded audit actions in order.
func (e *env) auditActions() []domain.CurationAction {
	var out []domain.CurationAction
	for _, a := range e.store.recorded() {
		out = append(out, a.Action)
	}
	return out
}

// assertInvalidateEffects checks the jobs, audit row and event an invalidate of f1 commits: refreshes for
// every model citing the fact or the deleted observation (and none for others), consolidate and graph.
func (e *env) assertInvalidateEffects() {
	e.t.Helper()
	t := e.t
	if keys := e.serialKeys(domain.OperationKindRefreshMentalModel); !slices.Equal(keys, []string{
		"mm:m-fact",
		"mm:m-obs",
	}) {
		t.Errorf("refresh jobs = %v, want mm:m-fact and mm:m-obs", keys)
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
	if evs := e.events(); len(evs) != 1 || evs[0].Type != domain.EventTypeMemoryCurated || evs[0].SubjectID != "f1" {
		t.Errorf("events = %+v", evs)
	}
}

func TestInvalidateRemovesCitingObservationsAndQueuesRefresh(t *testing.T) {
	e := newEnv(t)
	e.citing.by = map[port.UnitID][]port.MentalModelID{
		"f1": {"m-fact"}, "o1": {"m-obs", "m-fact"}, "f2": {"m-other"},
	}
	u := e.invalidate(e.curator())
	if u.ID != "f1" || u.State != domain.MemoryStateInvalidated || u.Embedding != nil {
		t.Fatalf("returned unit = %+v", u)
	}

	// Immediately (no job has run): neither the fact nor the observation citing it is visible to recall.
	e.assertNotLive("f1", "o1")
	semantic, keyword := e.searchIDs("Alice likes green tea", "Alice")
	for _, id := range []port.UnitID{"f1", "o1"} {
		if slices.Contains(semantic, id) || slices.Contains(keyword, id) {
			t.Errorf("search still returns %s: semantic %v, keyword %v", id, semantic, keyword)
		}
	}
	if f2, ok := e.unit("f2"); !ok || f2.ConsolidatedAt != nil {
		t.Errorf("co-source f2 = %+v (live %v), want consolidated_at reset", f2.ConsolidatedAt, ok)
	}

	e.assertInvalidateEffects()
}

// restoreF1 invalidates f1, drains the consolidation it queued and restores it, returning the rederive job.
func (e *env) restoreF1(c Curator) port.Job {
	e.t.Helper()
	e.invalidate(c)
	e.drain(domain.OperationKindConsolidate)
	u, op, err := c.Update(e.ctx, UpdateMemoryRequest{Bank: testBank, ID: "f1", State: domain.MemoryStateValid})
	if err != nil {
		e.t.Fatal(err)
	}
	if op == "" || u.State != domain.MemoryStateValid {
		e.t.Fatalf("restore = %+v, op %q", u, op)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	if len(jobs) != 1 || jobs[0].ID != op || jobs[0].SerialKey != "doc:d1" {
		e.t.Fatalf("rederive jobs = %+v", jobs)
	}
	return jobs[0]
}

// assertCausalF1F2 checks the causal edge f1 -> f2 exists.
func (e *env) assertCausalF1F2() {
	e.t.Helper()
	hits, err := e.fake.Read().ExpandGraph(e.ctx, port.GraphQuery{
		Bank: testBank, Seeds: []port.UnitID{"f1"}, MaxHops: 1, Kinds: []domain.LinkKind{domain.LinkCausal},
	})
	if err != nil || !slices.Contains(hitIDs(hits), "f2") {
		e.t.Errorf("causal link f1->f2 not reattached: %v %v", hitIDs(hits), err)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	e := newEnv(t)
	before, _ := e.unit("f1")
	job := e.restoreF1(e.curator())

	got, ok := e.unit("f1")
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
	if semantic, _ := e.searchIDs("Alice likes green tea", "Alice"); !slices.Contains(semantic, "f1") {
		t.Errorf("restored fact not searchable after rederive: %v", semantic)
	}
	if got, _ = e.unit("f1"); !slices.Equal(got.Entities, []string{"Alice"}) {
		t.Errorf("entities after rederive = %v, want [Alice]", got.Entities)
	}
	if len(e.linker.calls) != 2 || !slices.Equal(e.linker.calls[0], []port.UnitID{"f1"}) {
		t.Errorf("linker calls = %v", e.linker.calls)
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
func (e *env) rederiveEdited(names []string) {
	e.t.Helper()
	t := e.t
	if got, _ := e.unit("f1"); !slices.Equal(got.Entities, names) {
		t.Fatalf("entities committed by the RPC = %v, want %v", got.Entities, names)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	if len(jobs) != 1 {
		t.Fatalf("rederive jobs = %d", len(jobs))
	}
	var p RederivePayload
	if err := json.Unmarshal(jobs[0].Payload, &p); err != nil || p.UnitID != "f1" {
		t.Fatalf("payload = %s (%v)", jobs[0].Payload, err)
	}
	if err := e.rederiver(nil).Rederive(e.ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.unit("f1"); got.Embedding == nil || !slices.Equal(got.Entities, names) {
		t.Errorf("after rederive: embedding set %v, entities %v", got.Embedding != nil, got.Entities)
	}
}

func TestEditClearsEmbeddingAndQueuesRederive(t *testing.T) {
	e := newEnv(t)
	text := "Alice prefers oolong tea"
	u, op, err := e.curator().Update(e.ctx, UpdateMemoryRequest{
		Bank: testBank, ID: "f1", Text: &text, FactType: domain.FactTypeExperience,
		Entities: []string{"Alice", "Carol"}, Reason: "typo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op == "" || u.Text != text || u.Type != domain.FactTypeExperience || u.EditedAt == nil {
		t.Fatalf("edit = %+v op %q", u, op)
	}
	if got, _ := e.unit("f1"); got.Embedding != nil || got.Text != text || got.EditedAt == nil {
		t.Errorf("stored edit = %+v", got)
	}
	e.assertNotLive("o1")
	if audits := e.store.recorded(); len(audits) != 1 || audits[0].Action != domain.CurationEdit ||
		audits[0].OperationID != op || audits[0].Before == nil || audits[0].After == nil {
		t.Errorf("audit = %+v", audits)
	}
	e.rederiveEdited([]string{"Alice", "Carol"})
}

func TestEditClearEntities(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.curator().Update(e.ctx, UpdateMemoryRequest{Bank: testBank, ID: "f1", Entities: []string{}})
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
	if got, _ := e.unit("f1"); len(got.Entities) != 0 {
		t.Errorf("entities = %v, want cleared", got.Entities)
	}
}

func TestCuratorRejectsObservations(t *testing.T) {
	e := newEnv(t)
	c := e.curator()
	text := "x"
	empty := ""
	cases := []struct {
		name string
		req  UpdateMemoryRequest
		want error
	}{
		{"edit observation", UpdateMemoryRequest{ID: "o1", Text: &text}, domain.ErrInvalidArgument},
		{
			"invalidate observation",
			UpdateMemoryRequest{ID: "o1", State: domain.MemoryStateInvalidated},
			domain.ErrInvalidArgument,
		},
		{
			"invalidate with edits",
			UpdateMemoryRequest{ID: "f1", Text: &text, State: domain.MemoryStateInvalidated},
			domain.ErrInvalidArgument,
		},
		{"nothing to do", UpdateMemoryRequest{ID: "f1"}, domain.ErrInvalidArgument},
		{"empty text", UpdateMemoryRequest{ID: "f1", Text: &empty}, domain.ErrInvalidArgument},
		{
			"to observation",
			UpdateMemoryRequest{ID: "f1", FactType: domain.FactTypeObservation},
			domain.ErrInvalidArgument,
		},
		{"unknown unit", UpdateMemoryRequest{ID: "nope", Text: &text}, domain.ErrNotFound},
		{"restore unknown", UpdateMemoryRequest{ID: "nope", State: domain.MemoryStateValid}, domain.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Bank = testBank
			if _, _, err := c.Update(e.ctx, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, ok := e.unit("o1"); !ok {
		t.Error("a rejected curation changed the observation")
	}
	if n := len(e.ops(domain.OperationKindRederiveMemory)); n != 0 {
		t.Errorf("rejected curations enqueued %d rederives", n)
	}
}

func TestRestoreLiveUnitIsNoop(t *testing.T) {
	e := newEnv(t)
	u, op, err := e.curator().Update(e.ctx, UpdateMemoryRequest{
		Bank: testBank, ID: "f1", State: domain.MemoryStateValid,
	})
	if err != nil || op != "" || u.ID != "f1" {
		t.Fatalf("restore of a live unit = %+v %q %v", u, op, err)
	}
	if len(e.store.recorded()) != 0 {
		t.Error("no-op restore wrote an audit row")
	}
}

func TestRederiveSkipsWhenUnitGone(t *testing.T) {
	e := newEnv(t)
	e.invalidate(e.curator())
	e.drain(domain.OperationKindConsolidate)
	job := port.Job{Bank: testBank, Kind: domain.OperationKindRederiveMemory, Payload: json.RawMessage(`{"unit_id":"f1"}`)}
	if err := e.rederiver(nil).Rederive(e.ctx, job); err != nil {
		t.Fatal(err)
	}
	if n := len(e.claim(domain.OperationKindConsolidate)); n != 0 {
		t.Errorf("rederive of a gone unit enqueued %d consolidations", n)
	}
	bad := port.Job{Bank: testBank, Kind: domain.OperationKindRederiveMemory, Payload: json.RawMessage(`{}`)}
	if err := e.rederiver(nil).Rederive(e.ctx, bad); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("bad payload err = %v", err)
	}
}

// editingEmbedder edits the unit text while the rederive embeds, like a concurrent UpdateMemory.
type editingEmbedder struct {
	port.Embedder
	edit func()
}

// EmbedDocuments runs the edit, then embeds.
func (e editingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	e.edit()
	return e.Embedder.EmbedDocuments(ctx, texts)
}

func TestRederiveSkipsWhenTextChanged(t *testing.T) {
	e := newEnv(t)
	c := e.curator()
	first := "Alice likes jasmine tea"
	if _, _, err := c.Update(e.ctx, UpdateMemoryRequest{Bank: testBank, ID: "f1", Text: &first}); err != nil {
		t.Fatal(err)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	second := "Alice likes black tea"
	emb := editingEmbedder{Embedder: e.emb, edit: func() {
		if _, _, err := c.Update(e.ctx, UpdateMemoryRequest{Bank: testBank, ID: "f1", Text: &second}); err != nil {
			t.Error(err)
		}
	}}
	if err := e.rederiver(emb).Rederive(e.ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	got, _ := e.unit("f1")
	if got.Embedding != nil || got.Text != second {
		t.Errorf("stale rederive wrote an embedding for %q (text now %q)", first, got.Text)
	}
}
