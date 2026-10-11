package mentalmodel

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/reflect"
	"memd/internal/store"
	"strings"
	"testing"
	"time"
)

// fuzzySpec is a full single-shot model whose tag groups hold a fuzzy leaf "projct" nested in an any_of.
func fuzzySpec() domain.MentalModelSpec {
	leaf := domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{
		Tags: []string{"projct"}, Resolve: domain.TagResolveFuzzy,
	}}
	return domain.MentalModelSpec{ID: "status", Name: "Status", SourceQuery: "q", Trigger: domain.RefreshTrigger{
		Mode: domain.RefreshModeFull, Strategy: domain.RefreshStrategySingleShot,
		TagGroups: []domain.TagGroup{{Kind: domain.TagGroupAnyOf, Groups: []domain.TagGroup{leaf}}},
	}}
}

// TestFuzzyScopeResolvesBankTags checks decision 1203: a fuzzy leaf resolves against the bank's
// tags (Reader.TagCounts) before the scope compiles. An off-tag unit no longer stales the model,
// the misspelt leaf matches the real tag, an untagged unit still never stales it, and the runner's pre-check
// lets the refresh retrieve.
func TestFuzzyScopeResolvesBankTags(t *testing.T) {
	e := newEnv(t)
	m := e.addModel(fuzzySpec())
	detector := NewChangeDetector(e.store.Read(), e.reads, e.tags)
	e.addUnit(domain.FactTypeWorld, "untagged note")
	if stale, _, err := detector.NeedsRefresh(e.ctx, m); err != nil || stale {
		t.Fatalf("untagged unit staled a tag-grouped model: %v %v", stale, err)
	}
	e.addUnit(domain.FactTypeWorld, "Budget meeting moved", "marketing")
	if stale, _, err := detector.NeedsRefresh(e.ctx, m); err != nil || stale {
		t.Fatalf("an off-tag unit staled a fuzzy scope (tag filter dropped): %v %v", stale, err)
	}
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "project")
	if stale, _, err := detector.NeedsRefresh(e.ctx, m); err != nil || !stale {
		t.Fatalf("fuzzy scope missed a tagged unit: %v %v", stale, err)
	}
	if _, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true}); err != nil {
		t.Fatal(err)
	}
	if len(e.rc.reqs) != 1 {
		t.Fatalf("pre-check short-circuited a fuzzy scope: %d retrievals", len(e.rc.reqs))
	}
}

// TestAgenticRefreshSetsContext checks that an agentic refresh hands reflect the rendered
// mm_refresh_context: the full advice on the first build, the delta advice on a delta refresh.
func TestAgenticRefreshSetsContext(t *testing.T) {
	e := newEnv(t)
	e.addModel(deltaSpec("status", domain.RefreshStrategyAgentic))
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	if _, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true}); err != nil {
		t.Fatal(err)
	}
	e.addUnit(domain.FactTypeWorld, "Bob joins the project", "proj")
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON())
	if _, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true}); err != nil {
		t.Fatal(err)
	}
	if len(e.rf.reqs) != 2 {
		t.Fatalf("%d reflect calls, want 2", len(e.rf.reqs))
	}
	m := e.model("status")
	full, _ := refreshContext(m, domain.SeqUpTo(0, 1))
	delta, _ := refreshContext(m, domain.SeqUpTo(1, 2))
	if got := e.rf.reqs[0].Context; got == "" || got != full {
		t.Errorf("first build context = %q, want the full context %q", got, full)
	}
	if got := e.rf.reqs[1].Context; got != delta {
		t.Errorf("delta refresh context = %q, want the delta context %q", got, delta)
	}
}

// TestDeltaPreCheckReadsUntaggedUnits checks that the window pre-check uses the read scope: a tagged
// delta model with tags_match any refreshes from a new untagged unit instead of skipping it.
func TestDeltaPreCheckReadsUntaggedUnits(t *testing.T) {
	e := newEnv(t)
	m, _ := builtModel(e, domain.RefreshStrategySingleShot)
	m.Spec.Trigger.TagsMatch = domain.TagsMatchAny
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.addUnit(domain.FactTypeWorld, "Carol reviews the project")
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
		"kind": "append_block", "section_id": "status", "blocks_md": []string{"Carol reviews."},
	}))
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	calls := e.llm.CallsFor(domain.ModelSlotMentalModel, deltaPrompt)
	if out.Trace.EffectiveMode != domain.RefreshModeDelta || len(calls) != 1 ||
		out.Trace.Outcome != domain.RefreshOutcomeContentWritten {
		t.Fatalf("mode %s outcome %s delta calls %d", out.Trace.EffectiveMode, out.Trace.Outcome, len(calls))
	}
}

// TestAutomaticRunSkipsFreshOrFlooredModel checks that an automatic run re-checks staleness and the
// interval floor at start and finishes as NO_NEW_FACTS without any model call or retrieval.
func TestAutomaticRunSkipsFreshOrFlooredModel(t *testing.T) {
	e := newEnv(t)
	builtModel(e, domain.RefreshStrategySingleShot)
	assertSkipped := func(when string) {
		t.Helper()
		out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{OperationID: opID("op")})
		if err != nil || out.Trace.Outcome != domain.RefreshOutcomeNoNewFacts || out.VersionWritten != 0 {
			t.Fatalf("%s: err %v outcome %s version %d", when, err, out.Trace.Outcome, out.VersionWritten)
		}
		if e.llmCalls() != 0 || len(e.rc.reqs)+len(e.rf.reqs) != 0 {
			t.Fatalf("%s: %d llm calls, %d retrievals", when, e.llmCalls(), len(e.rc.reqs)+len(e.rf.reqs))
		}
	}
	assertSkipped("fresh")
	if fresh := e.model("status"); fresh.LastRefreshedAt != nil {
		t.Fatalf("fresh automatic run wrote last_refreshed_at %v", fresh.LastRefreshedAt)
	}

	floor, last := 3600, e.clock.Now()
	m := e.model("status")
	m.Spec.Trigger.MinRefreshIntervalSeconds, m.LastRefreshedAt = &floor, &last
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.clock.Advance(time.Minute)
	assertSkipped("inside the interval floor")
	got := e.model("status")
	if got.Version != 1 || got.LastSeqSeen != m.LastSeqSeen || got.LastRefreshedAt == nil ||
		!got.LastRefreshedAt.Equal(last) {
		t.Fatalf("floored run moved the model: v%d seq %d refreshed %v", got.Version, got.LastSeqSeen,
			got.LastRefreshedAt)
	}
	assertReparked(t, e, last.Add(time.Hour))
}

// assertReparked checks that a floored run re-parked its refresh: nothing is due before end, and at end
// exactly one automatic refresh of the model is claimable with run_at = end.
func assertReparked(t *testing.T, e *env, end time.Time) {
	t.Helper()
	if _, _, ok := claimRefresh(e); ok {
		t.Fatal("re-parked refresh due before the floor's end")
	}
	var ops []domain.Operation
	e.tx(func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.ctx, e.bank, store.OperationFilter{
			Kind: domain.OperationKindRefreshMentalModel, Status: domain.OperationStatusPending, PageSize: 10,
		})
		return err
	})
	if len(ops) != 1 || ops[0].NextRunAt == nil || !ops[0].NextRunAt.Equal(end) {
		t.Fatalf("pending refreshes %+v, want one with run_at %v", ops, end)
	}
	e.clock.Set(end)
	job, p, ok := claimRefresh(e)
	if !ok || job.ID != ops[0].ID || job.SerialKey != "mm:status" || p.Manual || p.ForceFull {
		t.Fatalf("re-parked job %v %+v payload %+v", ok, job, p)
	}
}

// citelessReflector answers with text but cites no memories.
type citelessReflector struct{}

// Reflect implements Reflector.
func (citelessReflector) Reflect(context.Context, reflect.Request) (reflect.Result, error) {
	return reflect.Result{Text: "## Status\n\nSomething uncited."}, nil
}

// TestAgenticAnswerWithoutCitationsFails checks that an agentic full answer citing nothing fails the run
// (no_citations) and writes nothing.
func TestAgenticAnswerWithoutCitationsFails(t *testing.T) {
	e := newEnv(t)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	spec := deltaSpec("status", domain.RefreshStrategyAgentic)
	spec.Trigger.Mode = domain.RefreshModeFull
	e.addModel(spec)
	md := ModelDeps{LLM: e.llm, Router: routingRouter(), Clock: e.clock}
	r := NewRefreshRunner(RefreshRunnerDeps{
		Store: e.store, Config: e.config, Scope: e.reads, Clock: e.clock,
		Synthesizers: []Synthesizer{NewAgenticSynthesizer(citelessReflector{}, md)},
	})
	out, err := r.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true})
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "no_citations") ||
		out.Trace.Outcome != domain.RefreshOutcomeFailed {
		t.Fatalf("err %v outcome %s", err, out.Trace.Outcome)
	}
	m := e.model("status")
	if m.Version != 0 || m.ContentMD != "" || m.LastSeqSeen != 0 || m.LastRefreshedAt != nil {
		t.Fatalf("failed run wrote the model: %+v", m)
	}
}

// TestRefreshContextRendersNameAndMode checks the agentic refresh's reflect context: the document name,
// and full versus delta advice by the window's lower bound.
func TestRefreshContextRendersNameAndMode(t *testing.T) {
	m := domain.MentalModel{Spec: domain.MentalModelSpec{Name: "Team roster"}}
	full, err := refreshContext(m, domain.SeqUpTo(0, 9))
	if err != nil {
		t.Fatal(err)
	}
	delta, err := refreshContext(m, domain.SeqUpTo(4, 9))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, `"Team roster"`) || !strings.Contains(full, "since when") ||
		strings.Contains(full, "only seeing information added") {
		t.Fatalf("full context: %q", full)
	}
	if !strings.Contains(delta, "only seeing information added") || strings.Contains(delta, "since when") {
		t.Fatalf("delta context: %q", delta)
	}
}

// TestFloorSkewTolerance checks that an automatic run with at most floorSkew of the interval floor left
// is not floored (it refreshes instead of re-parking), so clock skew can't make a re-park loop.
func TestFloorSkewTolerance(t *testing.T) {
	e := newEnv(t)
	builtModel(e, domain.RefreshStrategySingleShot)
	floor, last := 60, e.clock.Now()
	m := e.model("status")
	m.Spec.Trigger.MinRefreshIntervalSeconds, m.LastRefreshedAt = &floor, &last
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.clock.Set(last.Add(time.Minute - 3*time.Second))
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
		"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob joined."},
	}))
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil || out.VersionWritten != 2 {
		t.Fatalf("run within the skew tolerance was floored: v%d %v", out.VersionWritten, err)
	}
	if _, _, ok := claimRefresh(e); ok {
		t.Fatal("run within the skew tolerance re-parked")
	}
}
