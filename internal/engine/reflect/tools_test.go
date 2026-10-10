package reflect_test

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	rf "memd/internal/engine/reflect"
	mockreflect "memd/internal/mocks/engine/reflect"
	mockport "memd/internal/mocks/port"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
	"time"
)

// toolByName returns one of DefaultTools.
func toolByName(t *testing.T, tools []rf.Tool, name string) rf.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name() == name {
			if !json.Valid(tl.Schema()) || len(tl.Schema()) < 3 {
				t.Fatalf("%s has no schema", name)
			}
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

func newState(req rf.ReflectRequest) *rf.AgentState {
	req.Bank = bank
	return &rf.AgentState{Request: req, QueryTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestToolsRecallAndObservations(t *testing.T) {
	var got []recall.RecallRequest
	obs := unit("o1", "Alice is a tea person")
	obs.Type = domain.FactTypeObservation
	rc := &mockreflect.Recaller{}
	rc.RecallFunc = func(_ context.Context, req recall.RecallRequest) (recall.RecallResult, error) {
		got = append(got, req)
		if slices.Contains(req.Types, domain.FactTypeObservation) {
			return recall.RecallResult{Results: []recall.Memory{{Unit: obs, Stale: true}}}, nil
		}
		return recall.RecallResult{Results: []recall.Memory{{Unit: unit("u1", "Alice likes tea")}}}, nil
	}
	tools := rf.DefaultTools(rc, nil, nil, nil)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want recall and search_observations only", len(tools))
	}
	tags := domain.TagFilter{Tags: []string{"team-a"}}
	st := newState(rf.ReflectRequest{Tags: tags})
	st.Config.Reflect = &domain.ReflectConfig{SearchObservationsIncludeEntities: ptr(false)}

	res, err := toolByName(t, tools, rf.ToolRecall).Call(context.Background(), st, json.RawMessage(
		`{"reason":"x","query":"alice","max_tokens":50}`))
	if err != nil || res.Error != "" || !strings.Contains(string(res.Output), `"id":"u1"`) {
		t.Fatalf("recall: %s %v", res.Output, err)
	}
	checkRecallRequest(t, got[0])
	res, err = toolByName(t, tools, rf.ToolSearchObservations).Call(context.Background(), st, json.RawMessage(
		`{"reason":"x","query":"alice"}`))
	if err != nil || !strings.Contains(string(res.Output), `"is_stale":true`) {
		t.Fatalf("search_observations: %s %v", res.Output, err)
	}
	r := got[1]
	if r.MaxTokens != 5000 || !slices.Equal(r.Types, []domain.FactType{domain.FactTypeObservation}) ||
		r.Include.Entities == nil || *r.Include.Entities {
		t.Errorf("observations request = %+v", r)
	}
	_, fact := st.RetrievedUnits["u1"]
	_, observation := st.RetrievedUnits["o1"]
	if !fact || !observation || !st.Searched {
		t.Errorf("recall and search_observations must register what they retrieved")
	}
}

// checkRecallRequest checks what the recall tool asked recall for (max_tokens 50 clamped to 1000).
func checkRecallRequest(t *testing.T, r recall.RecallRequest) {
	t.Helper()
	facts := []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience}
	if r.MaxTokens != 1000 || !slices.Equal(r.Types, facts) || r.Budget != domain.BudgetMid ||
		r.Tags.Tags[0] != "team-a" || r.QueryTime == nil {
		t.Errorf("recall request = %+v", r)
	}
}

func TestToolsMentalModels(t *testing.T) {
	page := func(id, content string, tags ...string) domain.MentalModel {
		return domain.MentalModel{
			Spec:      domain.MentalModelSpec{ID: port.MentalModelID(id), Name: id, Tags: tags},
			ContentMD: content,
		}
	}
	all := []domain.MentalModel{
		page("best", "Best page in full."), page("second", strings.Repeat("x", 4400)),
		page("other-team", "Hidden.", "team-b"), page("excluded", "Excluded."),
	}
	pages := &mockreflect.PageReader{
		SearchPagesFunc: func(_ context.Context, _ port.BankRef, _ string, limit int) ([]domain.PageHit, error) {
			var hits []domain.PageHit
			for i, m := range all {
				hits = append(hits, domain.PageHit{MentalModelID: m.Spec.ID, Score: 1 - float64(i)/10})
			}
			return hits[:min(limit, len(hits))], nil
		},
		ReadPagesFunc: func(_ context.Context, _ port.BankRef, ids []port.MentalModelID) ([]domain.MentalModel, error) {
			var out []domain.MentalModel
			for _, m := range all {
				if slices.Contains(ids, m.Spec.ID) {
					out = append(out, m)
				}
			}
			return out, nil
		},
	}
	tools := rf.DefaultTools(nil, pages, nil, nil)
	st := newState(rf.ReflectRequest{
		Tags:                  domain.TagFilter{Tags: []string{"team-a"}, Match: domain.TagsMatchAny},
		ExcludeMentalModelIDs: []port.MentalModelID{"excluded"},
	})
	res, err := toolByName(t, tools, rf.ToolSearchMentalModels).Call(context.Background(), st,
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
		out.MentalModels[1].Content != "" || !strings.HasSuffix(out.MentalModels[1].Snippet, "...") {
		t.Errorf("search output = %s", res.Output)
	}
	if _, ok := st.RetrievedModels["second"]; !ok || len(st.RetrievedModels) != 2 {
		t.Errorf("retrieved = %v, want best and second (snippets count)", st.RetrievedModels)
	}
	st = newState(rf.ReflectRequest{ExcludeMentalModels: true})
	res, _ = toolByName(t, tools, rf.ToolSearchMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"query":"q"}`))
	if !strings.Contains(string(res.Output), `"count":0`) {
		t.Errorf("excluded mental models leaked: %s", res.Output)
	}
	testReadPages(t, tools)
}

// testReadPages checks read_mental_models' token budget and malformed-call handling.
func testReadPages(t *testing.T, tools []rf.Tool) {
	t.Helper()
	st := newState(rf.ReflectRequest{})
	res, err := toolByName(t, tools, rf.ToolReadMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"mental_model_ids":["second","best","missing"],"max_tokens":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Output), `"not_read":["best","missing"]`) || len(st.RetrievedModels) != 1 {
		t.Errorf("read output = %s (first page always read; budget then stops the rest)", res.Output)
	}
	res, _ = toolByName(t, tools, rf.ToolReadMentalModels).Call(context.Background(), st, json.RawMessage(`{}`))
	if res.Error == "" {
		t.Errorf("read without ids must go back to the model")
	}
	st = newState(rf.ReflectRequest{})
	if _, err := toolByName(t, tools, rf.ToolReadMentalModels).Call(context.Background(), st,
		json.RawMessage(`{"mental_model_ids":["missing"]}`)); err != nil || st.Searched {
		t.Errorf("reading no page is not a retrieval (searched=%v, err=%v)", st.Searched, err)
	}
}

func TestToolExpand(t *testing.T) {
	fact := unit("f1", "Alice ordered green tea")
	fact.ChunkID = "c1"
	obs := unit("o1", "Alice likes tea")
	obs.Type, obs.SourceIDs = domain.FactTypeObservation, []port.UnitID{"f1"}
	hidden := unit("h1", "Team B secret")
	hidden.Tags = []string{"team-b"}
	units := map[port.UnitID]domain.Unit{"f1": fact, "o1": obs, "h1": hidden}
	reader := &mockport.Reader{}
	reader.GetUnitsFunc = func(_ context.Context, _ port.BankRef, ids []port.UnitID) ([]domain.Unit, error) {
		var out []domain.Unit
		for _, id := range ids {
			if u, ok := units[id]; ok {
				out = append(out, u)
			}
		}
		return out, nil
	}
	chunks := &mockreflect.ChunkReader{}
	chunks.GetChunksFunc = func(context.Context, port.BankRef, []port.ChunkID) ([]domain.Chunk, error) {
		return []domain.Chunk{{ID: "c1", Text: "Chunk: Alice ordered green tea at noon.", DocumentID: "d1"}}, nil
	}
	tools := rf.DefaultTools(nil, nil, reader, chunks)
	st := newState(rf.ReflectRequest{Tags: domain.TagFilter{Tags: []string{"team-a"}, Match: domain.TagsMatchAny}})
	res, err := toolByName(t, tools, rf.ToolExpand).Call(context.Background(), st,
		json.RawMessage(`{"memory_ids":["o1","f1","h1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Output)
	if !containsAll(out, `"source_facts":[{"id":"f1"`, "Chunk: Alice ordered green tea at noon.",
		`"memory_id":"h1","error":"Memory not found: h1"`) || strings.Contains(out, "secret") {
		t.Errorf("expand output = %s", out)
	}
	if len(st.RetrievedUnits) != 0 {
		t.Errorf("expand must not register citable ids")
	}
	st = newState(rf.ReflectRequest{Types: []domain.FactType{domain.FactTypeWorld}})
	res, err = toolByName(t, tools, rf.ToolExpand).Call(context.Background(), st,
		json.RawMessage(`{"memory_ids":["o1","f1"]}`))
	if err != nil || !containsAll(string(res.Output), "Memory not found: o1", `"memory_id":"f1","memory"`) {
		t.Errorf("expand must filter units by the request's types: %s %v", res.Output, err)
	}
}
