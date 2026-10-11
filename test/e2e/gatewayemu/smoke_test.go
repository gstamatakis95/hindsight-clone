package gatewayemu_test

import (
	"encoding/json"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
	"testing"
	"time"
)

// mustTime parses an RFC 3339 time.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// structuredCase is the slot and user message a structured prompt is sent with.
type structuredCase struct {
	slot domain.ModelSlot
	user string
}

// structuredCases renders the user message of every structured prompt memd sends, keyed by system template.
func structuredCases(t *testing.T) map[string]structuredCase {
	t.Helper()
	extract := render(t, prompts.ExtractUser, prompts.ExtractData{
		Mission: "Track people.", Context: "chat", Text: "Alice moved to Paris. She loves Go! Bob plays chess.",
	})
	delta := prompts.DeltaData{
		SourceQuery: "Where do people live?",
		Document: prompts.DeltaDocument{Sections: []prompts.DeltaSection{{
			ID: "s1", Heading: "People", Level: 2, Blocks: []prompts.DeltaBlock{{ID: "b1", Text: "Carol lives in Rome."}},
		}}},
		Facts: sampleFacts(),
	}
	cases := map[string]structuredCase{
		prompts.ConsolidateSystem: {domain.ModelSlotConsolidate, render(t, prompts.ConsolidateUser,
			prompts.ConsolidateData{Facts: sampleFacts()})},
		prompts.DedupSystem: {domain.ModelSlotDedupCheck, render(t, prompts.DedupUser,
			prompts.DedupData{New: "Alice lives in Paris.", Existing: "Alice moved to Paris."})},
		"time_parse_system": {domain.ModelSlotTimeParse, "Reference date-time: 2024-03-01T10:00:00Z\nlast week"},
		prompts.MentalModelStructuredDeltaSystem: {
			domain.ModelSlotMentalModelDelta,
			render(t, prompts.MentalModelStructuredDeltaUser, delta),
		},
	}
	for _, name := range []string{
		prompts.ExtractConcise, prompts.ExtractVerbose, prompts.ExtractVerbatim, prompts.ExtractCustom,
	} {
		cases[name] = structuredCase{domain.ModelSlotExtract, extract}
	}
	return cases
}

// TestSmokeEveryPromptSchema sends every prompt that has a response schema through the real gateway client
// and validates the emulator's answer against that schema with internal/jsonschema. A schema added later
// without a case here fails the test, so the emulator keeps up with internal/prompts.
func TestSmokeEveryPromptSchema(t *testing.T) {
	_, client := newEmu(t)
	cases := structuredCases(t)
	seen := 0
	for _, name := range prompts.Names() {
		schema, err := prompts.Schema(name)
		if err != nil {
			continue // not a structured prompt
		}
		seen++
		t.Run(name, func(t *testing.T) {
			c, ok := cases[name]
			if !ok {
				t.Fatalf("no smoke case for structured prompt %s: add one and teach the emulator its semantics", name)
			}
			out := structured(t, client, ai.StructuredRequest{
				Slot: c.slot, Binding: binding(c.slot), Name: name, System: "system",
				User: c.user, Schema: schema,
			})
			mustValidate(t, name, schema, out)
		})
	}
	if seen != len(cases) {
		t.Fatalf("%d structured prompts in internal/prompts, %d smoke cases", seen, len(cases))
	}
}

// TestSmokeExtractOptionalDimensions covers every extraction schema in optional-dimensions mode and checks the
// one-fact-per-sentence semantics (one entry for verbatim).
func TestSmokeExtractOptionalDimensions(t *testing.T) {
	_, client := newEmu(t)
	user := render(t, prompts.ExtractUser, prompts.ExtractData{
		Text: "Alice moved to Paris. She loves Go! Bob plays chess.",
	})
	for _, name := range []string{
		prompts.ExtractConcise, prompts.ExtractVerbose, prompts.ExtractVerbatim, prompts.ExtractCustom,
	} {
		for _, optional := range []bool{false, true} {
			schema, err := prompts.ExtractSchema(name, optional)
			if err != nil {
				t.Fatal(err)
			}
			out := structured(t, client, ai.StructuredRequest{
				Slot: domain.ModelSlotExtract, Binding: binding(domain.ModelSlotExtract), Name: name, User: user, Schema: schema,
			})
			mustValidate(t, name, schema, out)
			var got struct {
				Facts []map[string]any `json:"facts"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			want := []string{"Alice moved to Paris.", "She loves Go!", "Bob plays chess."}
			if name == prompts.ExtractVerbatim {
				if len(got.Facts) != 1 {
					t.Fatalf("%s: %d entries, want 1", name, len(got.Facts))
				}
				continue
			}
			if len(got.Facts) != len(want) {
				t.Fatalf("%s: %d facts, want %d: %s", name, len(got.Facts), len(want), out)
			}
			for i, w := range want {
				if got.Facts[i]["what"] != w {
					t.Errorf("%s fact %d: what = %v, want %q", name, i, got.Facts[i]["what"], w)
				}
			}
		}
	}
}

// TestSmokeStructuredOutputCallerSchemas projects an answer into caller schemas using every keyword
// internal/jsonschema enforces.
func TestSmokeStructuredOutputCallerSchemas(t *testing.T) {
	_, client := newEmu(t)
	schemas := map[string]string{
		"flat": `{"type":"object","properties":{"answer":{"type":"string"},"n":{"type":"integer"},` +
			`"ok":{"type":"boolean"},"score":{"type":"number","minimum":2,"maximum":3}},"required":["answer","n"]}`,
		"bounds": `{"type":"object","additionalProperties":false,"required":["tags","code","kind","maybe"],"properties":{` +
			`"tags":{"type":"array","items":{"type":"string","minLength":3},"minItems":2,"maxItems":4},` +
			`"code":{"type":"string","maxLength":5},"kind":{"enum":["a","b"]},"maybe":{"type":["null","string"]}}}`,
		"refs": `{"$defs":{"p":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},` +
			`"type":"object","properties":{"people":{"type":"array","items":{"$ref":"#/$defs/p"}},` +
			`"either":{"anyOf":[{"type":"integer"},{"type":"string"}]},"one":{"oneOf":[{"const":"x"}]},` +
			`"both":{"allOf":[{"properties":{"a":{"type":"string"}},"required":["a"]},` +
			`{"properties":{"b":{"type":"integer"}},"required":["b"]}]}},"required":["people","either","one","both"]}`,
	}
	for name, raw := range schemas {
		schema := json.RawMessage(raw)
		user := render(t, prompts.StructuredOutputUser, prompts.StructuredOutputData{
			Document: "Alice lives in Paris.", Schema: raw,
		})
		out := structured(t, client, ai.StructuredRequest{
			Slot: domain.ModelSlotStructuredOutput, Binding: binding(domain.ModelSlotStructuredOutput),
			Name: prompts.StructuredOutputSystem, User: user, Schema: schema,
		})
		mustValidate(t, name, schema, out)
		if name == "flat" && !strings.Contains(string(out), `"answer":"Alice lives in Paris."`) {
			t.Errorf("flat: strings should quote the answer: %s", out)
		}
	}
}

// TestConsolidateOnePerFact checks the CONSOLIDATE semantics: one create per fact, citing its id, its text
// the fact's exact text.
func TestConsolidateOnePerFact(t *testing.T) {
	_, client := newEmu(t)
	schema, err := prompts.Schema(prompts.ConsolidateSystem)
	if err != nil {
		t.Fatal(err)
	}
	facts := sampleFacts()
	var got struct {
		Creates []struct {
			Text          string   `json:"text"`
			SourceFactIDs []string `json:"source_fact_ids"`
		} `json:"creates"`
		Updates []any `json:"updates"`
	}
	if _, err := client.Structured(t.Context(), ai.StructuredRequest{
		Slot: domain.ModelSlotConsolidate, Binding: binding(domain.ModelSlotConsolidate), Name: prompts.ConsolidateSystem,
		User: render(t, prompts.ConsolidateUser, prompts.ConsolidateData{Facts: facts}), Schema: schema,
	}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Creates) != len(facts) || len(got.Updates) != 0 {
		t.Fatalf("got %d creates, %d updates; want %d, 0", len(got.Creates), len(got.Updates), len(facts))
	}
	for i, f := range facts {
		c := got.Creates[i]
		if c.Text != f.Text || len(c.SourceFactIDs) != 1 || c.SourceFactIDs[0] != f.ID {
			t.Errorf("create %d = %+v, want text %q citing %s", i, c, f.Text, f.ID)
		}
	}
}

// TestSmokeReflectLoop drives the reflect protocol: a recall call first, then done quoting the recalled text
// and citing its id, for every done variant memd offers; a forced done answers at once.
func TestSmokeReflectLoop(t *testing.T) {
	_, client := newEmu(t)
	tools := reflectTools(t)
	variants := map[string]json.RawMessage{"plain": nil}
	for _, name := range []string{
		prompts.ReflectToolDoneDirectives, prompts.ReflectToolDoneDocument, prompts.ReflectToolDoneDocumentDirectives,
	} {
		raw, err := prompts.DoneTool(name, &prompts.ReflectData{})
		if err != nil {
			t.Fatal(err)
		}
		variants[name] = raw
	}
	for name, done := range variants {
		t.Run(name, func(t *testing.T) { reflectVariant(t, client, withDone(t, tools, done)) })
	}
	t.Run("forced_done", func(t *testing.T) {
		resp := chat(t, client, ai.ChatRequest{
			Messages: []ai.ChatMessage{{Role: ai.ChatRoleUser, Content: "Where does Alice live?"}},
			Tools:    tools, ToolChoice: "done",
		})
		args := doneArgs(t, resp, tools)
		if args["answer"] == "" {
			t.Fatal("forced done has an empty answer")
		}
	})
}

// reflectVariant runs the two turns of the loop with tools.
func reflectVariant(t *testing.T, client ai.LLM, tools []ai.ToolSpec) {
	t.Helper()
	msgs := []ai.ChatMessage{
		{Role: ai.ChatRoleSystem, Content: "system"}, {Role: ai.ChatRoleUser, Content: "Where does Alice live?"},
	}
	first := chat(t, client, ai.ChatRequest{Messages: msgs, Tools: tools, ToolChoice: "required"})
	if len(first.Message.ToolCalls) != 1 || first.Message.ToolCalls[0].Name != "recall" {
		t.Fatalf("first turn: %+v, want one recall call", first.Message)
	}
	call := first.Message.ToolCalls[0]
	var q struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(call.Arguments, &q); err != nil || q.Query != "Where does Alice live?" {
		t.Fatalf("recall arguments %s (%v)", call.Arguments, err)
	}
	output := `{"query":"Where does Alice live?",` +
		`"memories":[{"id":"u-1","text":"Alice lives in Paris.","fact_type":"world"}]}`
	msgs = append(msgs, first.Message, ai.ChatMessage{Role: ai.ChatRoleTool, ToolCallID: call.ID, Content: output})
	second := chat(t, client, ai.ChatRequest{Messages: msgs, Tools: tools, ToolChoice: "auto"})
	args := doneArgs(t, second, tools)
	text, _ := json.Marshal(args)
	if !strings.Contains(string(text), "Alice lives in Paris.") {
		t.Errorf("done does not quote the recalled text: %s", text)
	}
	if ids, _ := args["memory_ids"].([]any); len(ids) != 1 || ids[0] != "u-1" {
		t.Errorf("done memory_ids = %v, want [u-1]", args["memory_ids"])
	}
}

// withDone returns tools with done replaced by the given declaration (nil keeps the plain done).
func withDone(t *testing.T, tools []ai.ToolSpec, done json.RawMessage) []ai.ToolSpec {
	t.Helper()
	if done == nil {
		return tools
	}
	var spec ai.ToolSpec
	if err := json.Unmarshal(done, &spec); err != nil {
		t.Fatal(err)
	}
	out := make([]ai.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "done" {
			tool = spec
		}
		out = append(out, tool)
	}
	return out
}

// chat makes a REFLECT-slot chat call.
func chat(t *testing.T, client ai.LLM, req ai.ChatRequest) ai.ChatResponse {
	t.Helper()
	if req.Slot == "" {
		req.Slot, req.Binding = domain.ModelSlotReflect, binding(domain.ModelSlotReflect)
	}
	resp, err := client.Chat(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// doneArgs checks resp is one done call whose arguments match done's declared parameters, and decodes them.
func doneArgs(t *testing.T, resp ai.ChatResponse, tools []ai.ToolSpec) map[string]any {
	t.Helper()
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "done" {
		t.Fatalf("got %+v, want one done call", resp.Message)
	}
	args := resp.Message.ToolCalls[0].Arguments
	for _, tool := range tools {
		if tool.Name == "done" {
			mustValidate(t, "done arguments", tool.Parameters, args)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(args, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSmokePlainChat covers the tool-less chats: SYNTHESIZE, the markdown delta and the reflect final answer.
func TestSmokePlainChat(t *testing.T) {
	_, client := newEmu(t)
	facts := sampleFacts()
	cases := []struct {
		slot domain.ModelSlot
		user string
		want []string
	}{
		{domain.ModelSlotSynthesize, render(t, prompts.SynthesizeUser, prompts.SynthesizeData{
			Name: "People", SourceQuery: "Who lives where?", Facts: facts,
		}), []string{"## Who lives where?", facts[0].Text, facts[1].Text, facts[2].Text}},
		{domain.ModelSlotReflect, render(t, prompts.ReflectFinalUser, prompts.ReflectData{
			Query: "Where does Alice live?",
			Retrieved: []prompts.ToolOutput{{
				Tool: "recall", Output: `{"memories":[{"id":"u-1","text":"Alice lives in Paris."}]}`,
			}},
		}), []string{"Alice lives in Paris."}},
	}
	for _, c := range cases {
		resp := chat(t, client, ai.ChatRequest{
			Slot: c.slot, Binding: binding(c.slot),
			Messages: []ai.ChatMessage{{Role: ai.ChatRoleSystem, Content: "s"}, {Role: ai.ChatRoleUser, Content: c.user}},
		})
		for _, w := range c.want {
			if !strings.Contains(resp.Message.Content, w) {
				t.Errorf("%s: answer %q lacks %q", c.slot, resp.Message.Content, w)
			}
		}
	}
	empty := chat(t, client, ai.ChatRequest{
		Messages: []ai.ChatMessage{{Role: ai.ChatRoleUser, Content: "nothing retrieved"}},
	})
	if empty.Message.Content == "" {
		t.Error("reflect final with nothing retrieved returned an empty answer")
	}
}
