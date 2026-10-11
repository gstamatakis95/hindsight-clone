package mentalmodel

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/engine/reflect"
	"memd/internal/port"
	"testing"
	"time"
)

// stubProjector returns a fixed projection and records the document it saw.
type stubProjector struct{ doc string }

func (p *stubProjector) Project(_ context.Context, req reflect.ProjectRequest) (json.RawMessage, error) {
	p.doc = req.Document
	return json.RawMessage(`{"lead":"Alice"}`), nil
}

// TestStructuredOutputProjectedFromFinalPage checks that the page written is projected onto the
// trigger's schema and the projection is stored with the version.
func TestStructuredOutputProjectedFromFinalPage(t *testing.T) {
	e := newEnv(t)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Trigger.ResponseSchemaJSON = `{"type":"object","properties":{"lead":{"type":"string"}}}`
	spec.Trigger.KeepTrace = true
	e.addModel(spec)
	proj := &stubProjector{}
	md := ModelDeps{LLM: e.llm, Router: routingRouter(), Clock: e.clock}
	runner := NewRefreshRunner(RefreshRunnerDeps{
		Store: e.store, Config: e.config, Scope: e.reads, Clock: e.clock, Projector: proj,
		Synthesizers: []Synthesizer{NewSingleShotSynthesizer(e.rc, md)},
	})
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	if _, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	if string(m.StructuredOutputJSON) != `{"lead":"Alice"}` || proj.doc != m.ContentMD || m.LastTrace == nil {
		t.Fatalf("structured %s, projected %q, trace %v", m.StructuredOutputJSON, proj.doc, m.LastTrace)
	}
}

// TestMaxDeltaRatioFallsBackToFull checks that a delta changing more of the page than max_delta_ratio
// allows is replaced by a full synthesis.
func TestMaxDeltaRatioFallsBackToFull(t *testing.T) {
	e := newEnv(t)
	builtModel(e, domain.RefreshStrategySingleShot)
	ratio := 0.5
	m := e.model("status")
	m.Spec.Overrides = &domain.BankConfig{MentalModels: &domain.MentalModelConfig{MaxDeltaRatio: &ratio}}
	e.tx(func(tx port.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	doc := splitMarkdown(m.ContentMD)
	e.llm.RespondJSON(domain.ModelSlotMentalModelDelta, deltaPrompt, opsJSON(map[string]any{
		"kind": "replace_section_blocks", "section_id": doc.Sections[0].ID, "blocks_md": []string{"Rewritten."},
	}))
	e.scriptSynthesize("## Status\n\nAlice leads; Bob joined.")
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Trace.ModeFallbackReason != FallbackMaxDeltaRatio || out.Trace.EffectiveMode != domain.RefreshModeFull ||
		e.model("status").ContentMD != "## Status\n\nAlice leads; Bob joined." {
		t.Fatalf("trace %+v content %q", out.Trace, e.model("status").ContentMD)
	}
}

// TestRetractedSourceTriggersRefresh checks that invalidating a cited unit queues a refresh of the citing
// model only: "other" is stale (an in-scope write since it was built) but cites nothing, so it is not queued.
func TestRetractedSourceTriggersRefresh(t *testing.T) {
	e := newEnv(t)
	tr, _ := newTriggers(e)
	_, u := builtModel(e, domain.RefreshStrategySingleShot)
	e.addModel(domain.MentalModelSpec{ID: "other", Tags: []string{"proj"}})
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.archive(u.ID)
	if stale, _, _ := NewChangeDetector(e.store.Read(), e.reads).NeedsRefresh(e.ctx, e.model("other")); !stale {
		t.Fatal("other must be stale for this test to mean anything")
	}
	n, err := tr.Retracted(e.ctx, e.bank, []port.UnitID{u.ID})
	if err != nil || n != 1 {
		t.Fatalf("queued %d: %v", n, err)
	}
	e.clock.Advance(time.Minute) // past the debounce
	jobs, err := e.store.Claim(e.ctx, "w1", []port.JobKind{domain.OperationKindRefreshMentalModel}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claimed %d refreshes: %v", len(jobs), err)
	}
	var p RefreshPayload
	if err := json.Unmarshal(jobs[0].Payload, &p); err != nil || p.MentalModelID != "status" ||
		jobs[0].SerialKey != SerialKey("status") {
		t.Fatalf("queued the wrong model: %+v serial %q (%v)", p, jobs[0].SerialKey, err)
	}
}
