package observation

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"testing"
	"time"
)

// TestRunCreateAndUpdateWriteVersionsAndEvidence: a create writes version 1 citing its facts with exact
// quotes; an update rewrites the shown observation at version 2 and adds the new fact; every write
// carries a fresh change seq, and the run ends with a CONSOLIDATION_COMPLETED event.
func TestRunCreateAndUpdateWriteVersionsAndEvidence(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Bob plays chess", "x")
	e.observation("obs-chess", "Bob plays chess", []string{"x"}, "f0")
	e.fact("f1", "Bob plays chess every Sunday", "x")
	e.fact("f2", "Carol bakes bread", "x")
	before, err := e.store.Read().BankSeq(t.Context(), bank)
	if err != nil {
		t.Fatal(err)
	}
	e.script(ok(reply(
		[]create{{Text: "Carol bakes bread", SourceFactIDs: []string{"f2", "f2"}}},
		[]update{{ObservationID: "obs-chess", Text: "Bob plays chess every Sunday", SourceFactIDs: []string{"f1"}}},
	)))
	res := e.run(autoJob())
	user := e.calls(domain.ModelSlotConsolidate)[0].User
	if !strings.Contains(user, `"id":"`+string(uid("obs-chess"))+`"`) ||
		!strings.Contains(user, `"source_memories":[{"text":"Bob plays chess"}]`) {
		t.Errorf("the related observation and its source are not shown: %s", user)
	}
	got := map[domain.UnitID]string{}
	for _, o := range e.observations() {
		got[domain.UnitID(pgtest.Name(o.ID))] = fmt.Sprintf("%s|v%d|proof %d|%v|%v|new seq %t",
			o.Text, o.Version, o.ProofCount, sortedNames(o.SourceIDs), o.Tags, o.Seq > before)
	}
	want := map[domain.UnitID]string{
		"obs-chess": "Bob plays chess every Sunday|v2|proof 2|[f0 f1]|[x]|new seq true",
		"obs-01":    "Carol bakes bread|v1|proof 1|[f2]|[x]|new seq true",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("observations = %v, want %v", got, want)
	}
	if res.Processed != 2 || len(res.Changed) != 1 {
		t.Fatalf("result %+v", res)
	}
	var events []domain.LifecycleEvent
	e.tx(func(tx store.Tx) error {
		var err error
		events, _, err = tx.Events().List(t.Context(), bank, before, 10, []domain.EventType{
			domain.EventTypeConsolidationCompleted,
		})
		return err
	})
	if len(events) != 1 || pgtest.Name(events[0].OperationID) != "op-1" {
		t.Fatalf("events = %+v, want one CONSOLIDATION_COMPLETED for op-1", events)
	}
	want0 := domain.Usage{Slot: domain.ModelSlotConsolidate, Model: "base-model", InputTokens: 10, OutputTokens: 2}
	if len(res.Usage) != 1 || res.Usage[0] != want0 {
		t.Fatalf("usage = %+v, want [%+v]", res.Usage, want0)
	}
}

// TestConsolidateSchemaInjection: each call's schema pins source_fact_ids to the batch's ids (minItems 1,
// creates and updates) and caps creates at the remaining capacity.
func TestConsolidateSchemaInjection(t *testing.T) {
	raw, err := consolidateSchema([]string{"f1", "f2"}, ptr(3))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for _, list := range []string{"creates", "updates"} {
		src, ok := dig(root, "properties", list, "items", "properties", "source_fact_ids")
		if !ok {
			t.Fatalf("%s: no source_fact_ids", list)
		}
		items, _ := src["items"].(map[string]any)
		enum, _ := items["enum"].([]any)
		if len(enum) != 2 || enum[0] != "f1" || enum[1] != "f2" || src["minItems"] != float64(1) {
			t.Errorf("%s source_fact_ids = %v", list, src)
		}
	}
	creates, _ := dig(root, "properties", "creates")
	updates, _ := dig(root, "properties", "updates")
	if creates["maxItems"] != float64(3) || updates["maxItems"] != nil {
		t.Errorf("maxItems: creates %v updates %v", creates["maxItems"], updates["maxItems"])
	}
	raw, err = consolidateSchema([]string{"f1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "maxItems") {
		t.Errorf("uncapped schema has maxItems: %s", raw)
	}
}

// TestRunObservationCap: a full scope gets maxItems 0 and the limit note, so the model may only update.
func TestRunObservationCap(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{MaxObservationsPerScope: ptr(1)}})
	e.fact("f0", "Dana runs", "x")
	e.observation("obs-run", "Dana runs", []string{"x"}, "f0")
	e.fact("f1", "Dana swims", "x")
	e.script(ok(reply(nil, []update{{
		ObservationID: "obs-run", Text: "Dana runs and swims", SourceFactIDs: []string{"f1"},
	}})))
	e.run(autoJob())
	call := e.calls(domain.ModelSlotConsolidate)[0]
	if !strings.Contains(call.User, "OBSERVATION LIMIT REACHED (1/1)") ||
		!strings.Contains(string(call.Schema), `"maxItems":0`) {
		t.Errorf("no capacity note or maxItems 0: %s %s", call.User, call.Schema)
	}
	if obs := e.observations(); len(obs) != 1 || obs[0].Text != "Dana runs and swims" {
		t.Fatalf("observations = %+v", obs)
	}
	// A create past the cap fails the schema (maxItems 0); the unit fails alone and nothing succeeds after
	// it, so the run fails and nothing is parked.
	e.fact("f2", "Dana cycles", "x")
	e.script(ok(reply([]create{{Text: "Dana cycles", SourceFactIDs: []string{"f2"}}}, nil)))
	if _, err := e.consolidator().Run(t.Context(), e.jobRun(autoJob())); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("over-cap create: err = %v", err)
	}
}

// TestRunDropsInvalidActions: updates of unshown observations and actions citing no batch fact are
// dropped; their facts are still stamped (decided as none).
func TestRunDropsInvalidActions(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Eve paints", "x")
	e.script(ok(reply(
		[]create{{Text: "   ", SourceFactIDs: []string{"f1"}}},
		[]update{{ObservationID: "obs-unknown", Text: "Eve paints", SourceFactIDs: []string{"f1"}}},
	)))
	e.run(autoJob())
	if len(e.observations()) != 0 || e.unit("f1").ConsolidatedAt == nil {
		t.Fatal("want no observation and f1 stamped")
	}
}

// TestRunEmptyPayloadDisabledAndBadPayload: {} and an empty payload run every scope; disabled observations
// do nothing; a malformed payload is INVALID_ARGUMENT.
func TestRunEmptyPayloadDisabledAndBadPayload(t *testing.T) {
	e := newEnv(t, domain.BankConfig{Observations: &domain.ObservationConfig{Enabled: ptr(false)}})
	e.fact("f1", "Frank sings", "x")
	j := autoJob()
	j.Payload = nil
	res, err := e.consolidator().Run(t.Context(), e.jobRun(j))
	if err != nil || res.Processed != 0 || len(e.calls(domain.ModelSlotConsolidate)) != 0 {
		t.Fatalf("disabled: %+v, %v", res, err)
	}
	e.script(e.createEach("f1"))
	j.Overrides = &domain.BankConfig{Observations: &domain.ObservationConfig{Enabled: ptr(true)}}
	if res, err = e.consolidator().Run(t.Context(), e.jobRun(j)); err != nil || res.Processed != 1 {
		t.Fatalf("enabled by the request: %+v, %v", res, err)
	}
	j.Payload = json.RawMessage(`{"scopes": 5}`)
	if _, err := e.consolidator().Run(t.Context(), e.jobRun(j)); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("bad payload: err = %v", err)
	}
}

// TestRunManualScopes: a manual run takes only units in its scopes, but decides each in every scope it
// belongs to before stamping it.
func TestRunManualScopes(t *testing.T) {
	e := newEnv(t, domain.BankConfig{
		Retain: &domain.RetainConfig{
			DefaultObservationScopeModes: &domain.ObservationScopeModes{Mode: domain.ScopeModePerTag},
		},
	})
	e.fact("f1", "Gina codes", "a", "b")
	e.fact("f2", "Hank reads", "c")
	j := newJob([]domain.TagSet{{Tags: []string{"A"}}}, nil)
	e.script(e.createEach("f1"))
	res := e.run(j)
	if res.Processed != 1 || e.unit("f2").ConsolidatedAt != nil || e.unit("f1").ConsolidatedAt == nil {
		t.Fatalf("result %+v; want only f1", res)
	}
	if got := len(e.observations()); got != 2 {
		t.Fatalf("%d observations, want f1 in scopes a and b", got)
	}
	if res.More {
		t.Fatal("More for a scope with nothing left")
	}
}

// TestRunDocumentScopeMode: a unit's document overrides (the retain item's observation_scopes) beat the
// bank default.
func TestRunDocumentScopeMode(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.tx(func(tx store.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{
			ID: "d1", Bank: bank, Tags: []string{"a", "b"},
			Overrides: &domain.BankConfig{
				Retain: &domain.RetainConfig{
					DefaultObservationScopeModes: &domain.ObservationScopeModes{Mode: domain.ScopeModeShared},
				},
			},
		})
	})
	e.texts["f1"] = "Ivy hikes"
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: uid("f1"), Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Ivy hikes",
			Tags: []string{"a", "b"}, Embedding: e.emb.Vector("Ivy hikes"), Seq: seq,
		}})
	})
	e.script(e.createEach("f1"))
	e.run(autoJob())
	obs := e.observations()
	if len(obs) != 1 || len(obs[0].Tags) != 0 {
		t.Fatalf("observations = %+v, want one in the shared scope", obs)
	}
}

// addModels stores mental models in the store; the run lists them with Reader.ListMentalModels.
func (e *env) addModels(ms ...domain.MentalModel) {
	e.t.Helper()
	e.tx(func(tx store.Tx) error {
		for _, m := range ms {
			if err := tx.MentalModels().Create(e.t.Context(), m); err != nil {
				return err
			}
		}
		return nil
	})
}

// TestRunTriggersRefreshes: models with schedule.after_consolidation whose scope a changed scope reaches
// get a debounced refresh (or one parked at the end of their interval floor); paused, cron and
// unreached models do not.
func TestRunTriggersRefreshes(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	now := e.clock.Now()
	last := now.Add(-10 * time.Second)
	after := domain.RefreshSchedule{AfterConsolidation: true}
	floor := after
	floor.MinIntervalSeconds = ptr(120)
	model := func(id string, tags []string, sc domain.RefreshSchedule) domain.MentalModel {
		return domain.MentalModel{
			Spec: domain.MentalModelSpec{ID: domain.MentalModelID(id), Tags: tags, Schedule: sc},
			Bank: bank, LastRefreshedAt: &last,
		}
	}
	paused := model("paused", []string{"x"}, after)
	paused.AutoRefreshPaused = true
	e.addModels(
		model("tagged", []string{"x"}, after),
		model("untagged", nil, after),
		model("floor", []string{"x"}, floor),
		model("other", []string{"y"}, after),
		model("cron", []string{"x"}, domain.RefreshSchedule{Cron: "0 * * * *"}),
		paused,
	)
	e.fact("f1", "Jack climbs", "x")
	e.script(e.createEach("f1"))
	e.run(autoJob())
	got := map[string]time.Time{}
	e.clock.Advance(time.Hour)
	jobs, err := e.store.Claim(t.Context(), "w", []domain.OperationKind{domain.OperationKindRefreshMentalModel}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		// The wire shape is the refresh handler's (jobs.RefreshPayload): exactly the key
		// mental_model_id. Decoded by hand so a drift in this package's type is caught.
		var keys map[string]string
		if err := json.Unmarshal(j.Payload, &keys); err != nil {
			t.Fatal(err)
		}
		id := keys["mental_model_id"]
		if len(keys) != 1 || id == "" || j.SerialKey != "mm:"+id || j.Overrides != nil {
			t.Errorf("refresh job %+v payload %s", j, j.Payload)
		}
		got[id] = j.RunAt
	}
	want := map[string]time.Time{
		"tagged": now.Add(30 * time.Second), "untagged": now.Add(30 * time.Second), "floor": last.Add(120 * time.Second),
	}
	if len(got) != len(want) {
		t.Fatalf("refreshes = %v, want %v", got, want)
	}
	for id, at := range want {
		if !got[id].Equal(at) {
			t.Errorf("refresh %s at %v, want %v", id, got[id], at)
		}
	}
}

// TestModelReaches: the shared scope never reaches a tagged model; tag groups are AND-ed.
func TestModelReaches(t *testing.T) {
	m := func(tags []string, sc domain.MentalModelScope) domain.MentalModel {
		return domain.MentalModel{Spec: domain.MentalModelSpec{Tags: tags, Scope: sc}}
	}
	trig := func(f domain.TagFilter) domain.MentalModelScope { return domain.MentalModelScope{Tags: f} }
	group := domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"x"}}}
	tests := []struct {
		name  string
		model domain.MentalModel
		tags  []string
		want  bool
	}{
		{"untagged model, shared scope", m(nil, domain.MentalModelScope{}), nil, true},
		{"untagged model, tagged scope", m(nil, domain.MentalModelScope{}), []string{"x"}, true},
		{"tagged model, shared scope", m([]string{"x"}, domain.MentalModelScope{}), nil, false},
		{"tagged model, containing scope", m([]string{"x"}, domain.MentalModelScope{}), []string{"x", "y"}, true},
		{"tagged model, other scope", m([]string{"x"}, domain.MentalModelScope{}), []string{"y"}, false},
		{"exact mode", m([]string{"x"}, trig(domain.TagFilter{Match: domain.TagsMatchExact})), []string{"x", "y"}, false},
		{"tag groups", m(nil, trig(domain.TagFilter{Groups: []domain.TagGroup{group}})), []string{"x"}, true},
		{"tag groups, shared", m(nil, trig(domain.TagFilter{Groups: []domain.TagGroup{group}})), nil, false},
	}
	for _, tc := range tests {
		if got := modelReaches(tc.model, tc.tags); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
