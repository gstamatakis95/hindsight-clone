package reflect

import (
	"context"
	"encoding/json"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	mockai "memd/internal/mocks/ai"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"testing"
	"time"
)

// toolBank is the bank the tool tests read.
var toolBank = domain.BankRef{Tenant: "t1", Bank: "b1"}

// toolVector embeds every unit, page and query alike, so recall and page search return everything.
var toolVector = func() []float32 {
	v := make([]float32, domain.EmbeddingDim)
	v[0] = 1
	return v
}()

// toolEnv is a database holding toolBank and the tools built over a real recall engine.
type toolEnv struct {
	t     *testing.T
	store *postgres.DB
	tools []tool
}

// newToolEnv creates toolBank, stores units (embedded alike) and builds the tools.
func newToolEnv(t *testing.T, units ...domain.Unit) *toolEnv {
	t.Helper()
	e := &toolEnv{t: t, store: pgtest.NewStore(t)}
	e.tx(func(ctx context.Context, tx store.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, toolBank); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, toolBank)
		if err != nil || len(units) == 0 {
			return err
		}
		for i := range units {
			units[i].Bank, units[i].Seq, units[i].Embedding = toolBank, seq, toolVector
		}
		return tx.Units().Insert(ctx, units)
	})
	tok, err := ai.NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	emb := &mockai.Embedder{EmbedQueryFunc: func(context.Context, string) ([]float32, error) { return toolVector, nil }}
	r := e.store.Read()
	engine := recall.New(recall.Deps{
		DB: e.store, Config: config.NewResolver(e.store, domain.BankConfig{}), Embedder: emb, Router: ai.NewRouter(),
		Clock: clock.NewManual(time.Time{}), Tokens: tok,
		Arms: []recall.Arm{arms.NewVector(r), arms.NewText(r), arms.NewGraph(r, e.store.Graph()), arms.NewTemporal(r)},
	})
	e.tools = newTools(engine, e.store.Read())
	return e
}

// tx runs fn in a transaction and fails the test on error.
func (e *toolEnv) tx(fn func(ctx context.Context, tx store.Tx) error) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.store.InTx(ctx, func(tx store.Tx) error { return fn(ctx, tx) }); err != nil {
		e.t.Fatal(err)
	}
}

// tool returns the named tool, checking it declares a schema.
func (e *toolEnv) tool(name string) tool {
	e.t.Helper()
	for _, tl := range e.tools {
		if tl.Name() == name {
			if !json.Valid(tl.Schema()) || len(tl.Schema()) < 3 {
				e.t.Fatalf("%s has no schema", name)
			}
			return tl
		}
	}
	e.t.Fatalf("no tool %s", name)
	return nil
}

// newState returns a loop state for req over toolBank.
func newState(req Request) *agentState {
	req.Bank = toolBank
	return &agentState{Request: req, QueryTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// unitID is the unit id of a readable fixture name (Postgres stores unit ids as uuid).
func unitID(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

func TestToolsRecallAndObservations(t *testing.T) {
	obs := domain.Unit{ID: unitID("o1"), Type: domain.FactTypeObservation, Text: "Alice is a tea person"}
	fact := domain.Unit{ID: unitID("u1"), Type: domain.FactTypeWorld, Text: "Alice likes tea"}
	e := newToolEnv(t, fact, obs)
	st := newState(Request{Tags: domain.TagFilter{Tags: []string{"team-a"}}})
	st.Config.Reflect = &domain.ReflectConfig{SearchObservationsIncludeEntities: ptr(false)}

	res, err := e.tool(ToolRecall).Call(context.Background(), st, json.RawMessage(
		`{"reason":"x","query":"alice","max_tokens":50}`))
	if err != nil || res.Error != "" || !strings.Contains(string(res.Output), `"id":"`+string(fact.ID)+`"`) ||
		strings.Contains(string(res.Output), string(obs.ID)) {
		t.Fatalf("recall (raw facts only): %s %v", res.Output, err)
	}
	res, err = e.tool(ToolSearchObservations).Call(context.Background(), st, json.RawMessage(
		`{"reason":"x","query":"alice"}`))
	if err != nil || !strings.Contains(string(res.Output), `"is_stale":true`) ||
		strings.Contains(string(res.Output), string(fact.ID)) {
		t.Fatalf("search_observations (observations only, stale while u1 is unconsolidated): %s %v", res.Output, err)
	}
	_, gotFact := st.RetrievedUnits[fact.ID]
	_, gotObs := st.RetrievedUnits[obs.ID]
	if !gotFact || !gotObs || !st.Searched {
		t.Errorf("recall and search_observations must register what they retrieved")
	}
}

func TestToolsMentalModels(t *testing.T) {
	page := func(id, content string, tags ...string) domain.MentalModel {
		return domain.MentalModel{
			Spec:      domain.MentalModelSpec{ID: domain.MentalModelID(id), Name: id, Tags: tags},
			ContentMD: content,
		}
	}
	all := []domain.MentalModel{
		page("best", "Best page in full."), page("second", strings.Repeat("x", 4400)),
		page("other-team", "Hidden.", "team-b"), page("excluded", "Excluded."),
	}
	e := newToolEnv(t)
	for _, m := range all {
		e.tx(func(ctx context.Context, tx store.Tx) error {
			m.Bank = toolBank
			if err := tx.MentalModels().Create(ctx, m); err != nil {
				return err
			}
			m.Embedding = toolVector
			return tx.MentalModels().Update(ctx, m)
		})
	}
	st := newState(Request{
		Tags:                  domain.TagFilter{Tags: []string{"team-a"}, Match: domain.TagsMatchAny},
		ExcludeMentalModelIDs: []domain.MentalModelID{"excluded"},
	})
	res, err := e.tool(ToolSearchMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"query":"q"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		MentalModels []struct {
			ID, Content, Snippet string
		} `json:"mental_models"`
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.MentalModels) != 2 || out.MentalModels[0].Content != "Best page in full." ||
		out.MentalModels[1].Content != "" || !strings.HasSuffix(out.MentalModels[1].Snippet, "…") {
		t.Errorf("search output = %s", res.Output)
	}
	if _, ok := st.RetrievedModels["second"]; !ok || len(st.RetrievedModels) != 2 {
		t.Errorf("retrieved = %v, want best and second (snippets count)", st.RetrievedModels)
	}
	st = newState(Request{ExcludeMentalModels: true})
	res, _ = e.tool(ToolSearchMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"query":"q"}`))
	if !strings.Contains(string(res.Output), `"count":0`) {
		t.Errorf("excluded mental models leaked: %s", res.Output)
	}
	testReadPages(t, e)
}

// testReadPages checks read_mental_models' token budget and malformed-call handling.
func testReadPages(t *testing.T, e *toolEnv) {
	t.Helper()
	st := newState(Request{})
	res, err := e.tool(ToolReadMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"mental_model_ids":["second","best","missing"],"max_tokens":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Output), `"not_read":["best","missing"]`) || len(st.RetrievedModels) != 1 {
		t.Errorf("read output = %s (first page always read; budget then stops the rest)", res.Output)
	}
	res, _ = e.tool(ToolReadMentalModels).Call(context.Background(), st, json.RawMessage(`{}`))
	if res.Error == "" {
		t.Errorf("read without ids must go back to the model")
	}
	st = newState(Request{})
	if _, err := e.tool(ToolReadMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"mental_model_ids":["missing"]}`)); err != nil || st.Searched {
		t.Errorf("reading no page is not a retrieval (searched=%v, err=%v)", st.Searched, err)
	}
}

func TestToolExpand(t *testing.T) {
	// Postgres stores unit and chunk ids as uuid; ids maps the names in the JSON below to them.
	f1, o1, h1 := pgtest.ID[domain.UnitID]("f1"), pgtest.ID[domain.UnitID]("o1"), pgtest.ID[domain.UnitID]("h1")
	c1 := pgtest.ID[domain.ChunkID]("c1")
	ids := strings.NewReplacer("f1", string(f1), "o1", string(o1), "h1", string(h1)).Replace
	fact := domain.Unit{ID: f1, Bank: toolBank, Type: domain.FactTypeWorld, Text: "Alice ordered green tea"}
	fact.ChunkID = c1
	obs := domain.Unit{ID: o1, Bank: toolBank, Type: domain.FactTypeWorld, Text: "Alice likes tea"}
	obs.Type, obs.SourceIDs = domain.FactTypeObservation, []domain.UnitID{f1}
	hidden := domain.Unit{ID: h1, Bank: toolBank, Type: domain.FactTypeWorld, Text: "Team B secret"}
	hidden.Tags = []string{"team-b"}
	e := newToolEnv(t)
	ctx := context.Background()
	err := e.store.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, toolBank)
		if err != nil {
			return err
		}
		if err := tx.Documents().Upsert(ctx, domain.Document{ID: "d1", Bank: toolBank}); err != nil {
			return err
		}
		if err := tx.Chunks().Insert(ctx, []domain.Chunk{
			{ID: c1, Bank: toolBank, DocumentID: "d1", Text: "Chunk: Alice ordered green tea at noon."},
		}); err != nil {
			return err
		}
		fact.DocumentID, fact.Seq, hidden.Seq, obs.Seq = "d1", seq, seq, seq
		if err := tx.Units().Insert(ctx, []domain.Unit{fact, hidden}); err != nil {
			return err
		}
		obs.Tags = []string{"team-a"}
		if err := tx.Units().UpsertObservation(ctx, obs); err != nil {
			return err
		}
		return tx.Evidence().Add(ctx, o1, []domain.Evidence{{Unit: f1, Quote: fact.Text}})
	})
	if err != nil {
		t.Fatal(err)
	}
	st := newState(Request{Tags: domain.TagFilter{Tags: []string{"team-a"}, Match: domain.TagsMatchAny}})
	res, err := e.tool(ToolExpand).Call(context.Background(), st,
		json.RawMessage(ids(`{"memory_ids":["o1","f1","h1"]}`)))
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Output)
	if !containsAll(out, ids(`"source_facts":[{"id":"f1"`), "Chunk: Alice ordered green tea at noon.",
		ids(`"memory_id":"h1","error":"Memory not found: h1"`)) || strings.Contains(out, "secret") {
		t.Errorf("expand output = %s", out)
	}
	if len(st.RetrievedUnits) != 0 {
		t.Errorf("expand must not register citable ids")
	}
	st = newState(Request{Types: []domain.FactType{domain.FactTypeWorld}})
	res, err = e.tool(ToolExpand).Call(context.Background(), st,
		json.RawMessage(ids(`{"memory_ids":["o1","f1"]}`)))
	if err != nil || !containsAll(string(res.Output), ids("Memory not found: o1"), ids(`"memory_id":"f1","memory"`)) {
		t.Errorf("expand must filter units by the request's types: %s %v", res.Output, err)
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// containsAll reports whether s contains every part.
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
