package consolidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
)

// perTag is a bank config fanning units out one scope per tag.
func perTag() domain.BankConfig {
	return domain.BankConfig{DefaultObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag}}
}

// TestRunEditBetweenScopesIsNotStamped: a fact edited (Paris -> Rome) after its first scope committed is
// not quoted with its stale text in later scopes and is not stamped, so the next run re-decides it.
func TestRunEditBetweenScopesIsNotStamped(t *testing.T) {
	e := newEnv(t, perTag())
	e.fact("f1", "Alice lives in Paris", "a", "b")
	e.script(e.createEach("f1"))
	e.deps.Checkpoint = func(port.Job) Checkpointer {
		return &hookAt{n: 2, fn: func() {
			e.tx(func(tx port.Tx) error {
				u := e.unit("f1")
				seq, err := tx.Seq(t.Context(), bank)
				if err != nil {
					return err
				}
				u.Text, u.Seq, u.Embedding = "Alice lives in Rome", seq, nil
				return tx.Units().Update(t.Context(), u)
			})
		}}
	}
	e.run(autoJob())
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("an edited fact was stamped")
	}
	for _, o := range e.observations() {
		if o.Scope().String() == scopeOf("b").String() {
			t.Fatalf("scope b got an observation from the stale text: %q", o.Text)
		}
	}
}

// TestRunRetagMidRunIsNotStamped: a fact retagged (alice -> bob) mid-run is not written or stamped under
// its old scopes.
func TestRunRetagMidRunIsNotStamped(t *testing.T) {
	e := newEnv(t, perTag())
	e.tx(func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{ID: "d1", Bank: bank, Tags: []string{"alice", "x"}})
	})
	e.texts["f1"] = "Alice paints"
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: "f1", Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Alice paints",
			Tags: []string{"alice", "x"}, Embedding: e.emb.Vector("Alice paints"), Seq: seq,
		}})
	})
	e.script(e.createEach("f1"))
	e.deps.Checkpoint = func(port.Job) Checkpointer {
		return &hookAt{n: 2, fn: func() {
			e.tx(func(tx port.Tx) error {
				seq, err := tx.Seq(t.Context(), bank)
				if err != nil {
					return err
				}
				_, err = tx.Units().SetDocumentTags(t.Context(), bank, "d1", []string{"alice", "x"}, []string{"bob"}, seq)
				return err
			})
		}}
	}
	e.run(autoJob())
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("a retagged fact was stamped")
	}
	if got := len(e.observations()); got != 1 {
		t.Fatalf("%d observations, want only scope alice's (written before the retag)", got)
	}
}

// TestRunGoneTargetIsNotResurrected: an update whose observation is deleted between decide and apply is
// skipped (no new observation with its text), and the batch's facts stay unstamped.
func TestRunGoneTargetIsNotResurrected(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Bob plays chess", "x")
	e.observation("obs-chess", "Bob plays chess", []string{"x"}, "f0")
	e.fact("f1", "Bob plays chess on Sunday", "x")
	e.script(ok(reply(nil, []update{{
		ObservationID: "obs-chess", Text: "Bob plays chess on Sunday", SourceFactIDs: []string{"f1"},
	}})))
	e.deps.Embedder = &hookEmbedder{Embedder: e.emb, fn: func() {
		e.tx(func(tx port.Tx) error {
			_, _, err := tx.Evidence().DeleteCiting(t.Context(), bank, []port.UnitID{"f0"})
			return err
		})
	}}
	e.run(autoJob())
	if obs := e.observations(); len(obs) != 0 {
		t.Fatalf("observations = %+v, want none (the deleted one must not come back)", obs)
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("f1 stamped although its decision was dropped")
	}
}

// TestRunArchivedEvidenceSkipsAction: an action citing a fact archived since decide is skipped whole,
// never trimmed to its live evidence, and the batch stays unstamped.
func TestRunArchivedEvidenceSkipsAction(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Carol bakes bread", "x")
	e.fact("f2", "Carol bakes rye", "x")
	e.script(ok(reply([]create{{Text: "Carol bakes bread and rye", SourceFactIDs: []string{"f1", "f2"}}}, nil)))
	e.deps.Embedder = &hookEmbedder{Embedder: e.emb, fn: func() {
		e.tx(func(tx port.Tx) error { return tx.Units().Archive(t.Context(), bank, "f2", "wrong") })
	}}
	e.run(autoJob())
	if obs := e.observations(); len(obs) != 0 {
		t.Fatalf("observations = %+v, want none", obs)
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("f1 stamped although its action was skipped")
	}
}

// TestRunRefreshSurvivesFailedRun: a scope committed before the run fails has already queued the refresh
// of the models it reaches.
func TestRunRefreshSurvivesFailedRun(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.addModels(domain.MentalModel{
		Spec: domain.MentalModelSpec{ID: "ma", Tags: []string{"a"}, Trigger: domain.RefreshTrigger{
			RefreshAfterConsolidation: true,
		}},
		Bank: bank,
	})
	e.fact("f1", "Kim sails", "a")
	e.fact("f2", "Lee rows", "b")
	e.script(e.createEach("f1"), fail())
	if _, err := e.consolidator().Run(t.Context(), autoJob()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	e.wantPending(domain.OperationKindRefreshMentalModel, 1)
}

// TestRunRecheckCitedScopesBeforeStamp: a scope skipped at plan time because it cited the unit is checked
// again before stamping; if its observation was cleared meanwhile, the unit is not stamped.
func TestRunRecheckCitedScopesBeforeStamp(t *testing.T) {
	e := newEnv(t, perTag())
	e.fact("f1", "Max surfs", "a", "b")
	e.seedObservation("obs-a", "Max surfs", []string{"a"}, false, "f1")
	e.script(e.createEach("f1"))
	e.deps.Checkpoint = func(port.Job) Checkpointer {
		return &hookAt{n: 1, fn: func() {
			e.tx(func(tx port.Tx) error {
				_, _, err := tx.Evidence().DeleteCiting(t.Context(), bank, []port.UnitID{"f1"})
				return err
			})
		}}
	}
	e.run(autoJob())
	e.wantBatches([]string{"f1"})
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("stamped although scope a no longer cites it")
	}
}

// TestRunScopedContinuationNeverJoins: a manual run's continuation carries its scopes and is never
// deduplicated (it doesn't join the queued automatic consolidate).
func TestRunScopedContinuationNeverJoins(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationMaxUnitsPerRun: ptr(1)})
	e.fact("f1", "One", "a")
	e.fact("f2", "Two", "a")
	e.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(t.Context(), bank); err != nil {
			return err
		}
		_, _, err := tx.Queue().Enqueue(t.Context(), NewJob(bank, nil, nil))
		return err
	})
	e.script(e.createEach("f1"))
	j := NewJob(bank, []domain.TagSet{{Tags: []string{"a"}}}, nil)
	j.ID = "op-manual"
	if res := e.run(j); !res.More {
		t.Fatalf("result %+v, want More", res)
	}
	ops := e.jobs(domain.OperationKindConsolidate)
	if len(ops) != 2 {
		t.Fatalf("pending consolidate = %d, want the automatic one and the scoped continuation", len(ops))
	}
}

// TestFirstUpdatesKeepsFirst: a second update of one observation in a batch is dropped, not merged.
func TestFirstUpdatesKeepsFirst(t *testing.T) {
	ev := func(id port.UnitID) []domain.Evidence { return []domain.Evidence{{Unit: id, Quote: string(id)}} }
	in := []ConsolidationAction{
		{Kind: ActionUpdate, ObservationID: "o1", Text: "first", Evidence: ev("f1")},
		{Kind: ActionCreate, Text: "new", Evidence: ev("f3")},
		{Kind: ActionUpdate, ObservationID: "o1", Text: "second", Evidence: ev("f2")},
	}
	out, dropped := firstUpdates(in)
	if !dropped || len(out) != 2 || out[0].Text != "first" || len(out[0].Evidence) != 1 || out[1].Text != "new" {
		t.Fatalf("firstUpdates = %+v, %v", out, dropped)
	}
	if _, dropped := firstUpdates(in[:2]); dropped {
		t.Fatal("nothing to drop")
	}
}

// TestRunDoubleUpdateLeavesBatchUnstamped: when the model updates one observation twice, the first update
// lands and the batch's facts stay unstamped for the next run.
func TestRunDoubleUpdateLeavesBatchUnstamped(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Dana runs", "x")
	e.observation("obs-run", "Dana runs", []string{"x"}, "f0")
	e.fact("f1", "Dana runs daily", "x")
	e.fact("f2", "Dana runs fast", "x")
	e.script(ok(reply(nil, []update{
		{ObservationID: "obs-run", Text: "Dana runs daily", SourceFactIDs: []string{"f1"}},
		{ObservationID: "obs-run", Text: "Dana runs fast", SourceFactIDs: []string{"f2"}},
	})))
	e.run(autoJob())
	obs := e.observations()
	if len(obs) != 1 || obs[0].Text != "Dana runs daily" || !slices.Equal(obs[0].SourceIDs, []port.UnitID{"f0", "f1"}) {
		t.Fatalf("observations = %+v", obs)
	}
	if e.unit("f1").ConsolidatedAt != nil || e.unit("f2").ConsolidatedAt != nil {
		t.Fatal("batch stamped although part of its decision was dropped")
	}
}

// TestRunItemScopesBeatOverrides: a document's item-level observation_scopes (Document.ObservationScopes,
// stored by the fake's DocumentRepo.Upsert) beat its overrides' default.
func TestRunItemScopesBeatOverrides(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.tx(func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{
			ID: "d1", Bank: bank, Tags: []string{"a", "b"},
			Overrides: &domain.BankConfig{
				DefaultObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag},
			},
			ObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModeShared},
		})
	})
	e.texts["f1"] = "Ivy hikes"
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: "f1", Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Ivy hikes",
			Tags: []string{"a", "b"}, Embedding: e.emb.Vector("Ivy hikes"), Seq: seq,
		}})
	})
	e.script(e.createEach("f1"))
	e.run(autoJob())
	if obs := e.observations(); len(obs) != 1 || len(obs[0].Tags) != 0 {
		t.Fatalf("observations = %+v, want one in the shared scope", obs)
	}
}

// TestRunVersionsAndQuotesRecorded: every write records its observation_versions row (version, text, all
// sources) and cites each fact with its exact text.
func TestRunVersionsAndQuotesRecorded(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f0", "Bob plays chess", "x")
	e.observation("obs-chess", "Bob plays chess", []string{"x"}, "f0")
	e.fact("f1", "Bob plays chess every Sunday", "x")
	e.fact("f2", "Carol bakes bread", "x")
	rec := &recorder{}
	e.deps.Store = recStore{Store: e.store, rec: rec}
	e.script(ok(reply(
		[]create{{Text: "Carol bakes bread", SourceFactIDs: []string{"f2"}}},
		[]update{{ObservationID: "obs-chess", Text: "Bob plays chess every Sunday", SourceFactIDs: []string{"f1"}}},
	)))
	e.run(autoJob())
	var got []string
	for _, v := range rec.versions {
		got = append(got, fmt.Sprintf("%d|%s|%s", v.Version, v.Text, idsString(v.SourceIDs)))
	}
	want := []string{"2|Bob plays chess every Sunday|f0,f1", "1|Carol bakes bread|f2"}
	if !slices.Equal(got, want) {
		t.Fatalf("versions = %q, want %q", got, want)
	}
	for obs, evs := range rec.evidence {
		for _, ev := range evs {
			if ev.Quote != e.texts[ev.Unit] {
				t.Errorf("%s cites %s with %q, want its exact text %q", obs, ev.Unit, ev.Quote, e.texts[ev.Unit])
			}
		}
	}
	if len(rec.evidence) != 2 {
		t.Fatalf("evidence written for %d observations, want 2", len(rec.evidence))
	}
}

// TestScopeExpanderRejectsNonPositiveCap: max_combinations <= 0 is INVALID_ARGUMENT.
func TestScopeExpanderRejectsNonPositiveCap(t *testing.T) {
	for _, n := range []int{0, -1} {
		_, err := NewScopeExpander().Expand(bank, []string{"a"}, domain.ObservationScopes{
			Mode: domain.ScopeModeAllCombinations, MaxCombinations: ptr(n),
		})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("max_combinations %d: err = %v", n, err)
		}
	}
}

// idsString joins unit ids with commas.
func idsString(ids []port.UnitID) string {
	b, _ := json.Marshal(ids)
	return strings.NewReplacer(`"`, "", "[", "", "]", "").Replace(string(b))
}
