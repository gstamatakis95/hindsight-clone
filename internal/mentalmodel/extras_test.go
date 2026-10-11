package mentalmodel

import (
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/prompts"
	"memd/internal/store"
	"strings"
	"testing"
)

// TestStructuredOutputProjectedFromFinalPage checks that the page written is projected onto the
// trigger's schema and the projection is stored with the version.
func TestStructuredOutputProjectedFromFinalPage(t *testing.T) {
	e := newEnv(t)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Trigger.ResponseSchemaJSON = `{"type":"object","properties":{"lead":{"type":"string"}}}`
	spec.Trigger.KeepTrace = true
	e.addModel(spec)
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	e.llm.Script(domain.ModelSlotStructuredOutput, "", fakeJSON(`{"lead":"Alice"}`))
	if _, err := e.svc.refresh(e.ctx, e.bank, "status", refreshOptions{}); err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	var structured map[string]string // jsonb: compare the value, not its spacing
	if err := json.Unmarshal(m.StructuredOutputJSON, &structured); err != nil || structured["lead"] != "Alice" ||
		len(structured) != 1 || m.LastTrace == nil {
		t.Fatalf("structured %s, trace %v", m.StructuredOutputJSON, m.LastTrace)
	}
	calls := e.llm.CallsFor(domain.ModelSlotStructuredOutput, prompts.StructuredOutputSystem)
	if len(calls) != 1 || !strings.Contains(calls[0].Structured.User, m.ContentMD) {
		t.Fatalf("projection calls %d: the final page was not projected", len(calls))
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
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	doc := splitMarkdown(m.ContentMD)
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
		"kind": "replace_section_blocks", "section_id": doc.Sections[0].ID, "blocks_md": []string{"Rewritten."},
	}))
	e.scriptSynthesize("## Status\n\nAlice leads; Bob joined.")
	out, err := e.svc.refresh(e.ctx, e.bank, "status", refreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Trace.ModeFallbackReason != fallbackMaxDeltaRatio || out.Trace.EffectiveMode != domain.RefreshModeFull ||
		e.model("status").ContentMD != "## Status\n\nAlice leads; Bob joined." {
		t.Fatalf("trace %+v content %q", out.Trace, e.model("status").ContentMD)
	}
}
