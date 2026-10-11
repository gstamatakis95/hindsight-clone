package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"memd/internal/domain"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// dataFor maps every template to a constructor of its data, so each fixture is decoded into the struct the
// template reads (unknown fixture keys fail) and each template also renders with that struct's zero value.
// A constructor returning *map[string]any marks a template whose data is a plain map.
var dataFor = map[string]func() any{
	ExtractConcise:                   func() any { return &ExtractData{} },
	ExtractVerbose:                   func() any { return &ExtractData{} },
	ExtractVerbatim:                  func() any { return &ExtractData{} },
	ExtractCustom:                    func() any { return &ExtractData{} },
	ExtractUser:                      func() any { return &ExtractData{} },
	ConsolidateSystem:                func() any { return &ConsolidateData{} },
	ConsolidateUser:                  func() any { return &ConsolidateData{} },
	DedupSystem:                      func() any { return &DedupData{} },
	DedupUser:                        func() any { return &DedupData{} },
	ReflectSystem:                    func() any { return &ReflectData{} },
	ReflectDoneRequest:               func() any { return &ReflectData{} },
	ReflectFinal:                     func() any { return &ReflectData{} },
	ReflectFinalUser:                 func() any { return &ReflectData{} },
	StructuredOutputSystem:           func() any { return &StructuredOutputData{} },
	StructuredOutputUser:             func() any { return &StructuredOutputData{} },
	SynthesizeSystem:                 func() any { return &SynthesizeData{} },
	SynthesizeUser:                   func() any { return &SynthesizeData{} },
	MentalModelStructuredDeltaSystem: func() any { return &DeltaData{} },
	MentalModelStructuredDeltaUser:   func() any { return &DeltaData{} },
	MentalModelRefreshContext:        func() any { return &RefreshContextData{} },
	ReflectToolDirectiveRules:        func() any { return &ReflectData{} },
	ReflectToolErrorSearchFirst:      func() any { return &ReflectData{} },
	ReflectToolErrorUnknown:          func() any { return &ReflectData{} },
	ReflectToolErrorDone:             func() any { return &ReflectData{} },
	"time_parse_system":              func() any { return &map[string]any{} },
	"time_parse_user":                func() any { return &map[string]any{} },
}

// optionalTemplates may be registered before their template exists (owned by another work package).
var optionalTemplates = map[string]bool{"time_parse_system": true, "time_parse_user": true}

// emptyWhenZero are fragments that render nothing for zero data (the engine does not use them then).
var emptyWhenZero = map[string]bool{ReflectToolDirectiveRules: true}

// decodeFixture decodes testdata/<name>.json into the data type registered for name.
func decodeFixture(name string) (any, error) {
	newData, ok := dataFor[name]
	if !ok {
		return nil, fmt.Errorf("template %q has no entry in dataFor", name)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json")) //nolint:gosec // name is a template name, not input
	if err != nil {
		return nil, fmt.Errorf("missing fixture testdata/%s.json: %w", name, err)
	}
	data := newData()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(data); err != nil {
		return nil, fmt.Errorf("fixture testdata/%s.json: %w", name, err)
	}
	if m, ok := data.(*map[string]any); ok {
		return *m, nil
	}
	return data, nil
}

// checkOutput fails on empty output or a rendering hole.
func checkOutput(t *testing.T, out string) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty output")
	}
	for _, hole := range []string{"<no value>", "<nil>"} {
		if strings.Contains(out, hole) {
			t.Fatalf("output has %s:\n%s", hole, out)
		}
	}
}

// TestRegistryMatchesTemplates checks every template has a data type and every data type a template.
func TestRegistryMatchesTemplates(t *testing.T) {
	names := map[string]bool{}
	for _, name := range Names() {
		names[name] = true
		if _, ok := dataFor[name]; !ok {
			t.Errorf("template %q has no entry in dataFor", name)
		}
	}
	for name := range dataFor {
		if !names[name] && !optionalTemplates[name] {
			t.Errorf("dataFor has %q but there is no templates/%s.tmpl", name, name)
		}
	}
}

// TestEveryTemplateRendersWithFixture renders each template with testdata/<name>.json, decoded into its
// data type, and fails on a template without a fixture, an execution error, empty output or a hole.
func TestEveryTemplateRendersWithFixture(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no templates embedded")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			data, err := decodeFixture(name)
			if err != nil {
				t.Fatal(err)
			}
			out, err := Render(name, data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			checkOutput(t, out)
			if v := Version(name); len(v) != versionLen {
				t.Fatalf("Version = %q", v)
			}
		})
	}
}

// TestEveryTemplateRendersWithZeroData renders each struct-backed template with its data type's zero value,
// the branch where every optional block (labels, directives, disposition, ...) is off.
func TestEveryTemplateRendersWithZeroData(t *testing.T) {
	for _, name := range Names() {
		newData, ok := dataFor[name]
		if !ok {
			continue // reported by TestRegistryMatchesTemplates
		}
		data := newData()
		if _, isMap := data.(*map[string]any); isMap || emptyWhenZero[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			out, err := Render(name, data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			checkOutput(t, out)
		})
	}
}

// TestBranchVariants renders the branches neither the fixtures nor the zero values reach, so a misspelled
// field inside a rarely taken branch fails here rather than in production.
func TestBranchVariants(t *testing.T) {
	type variant struct {
		name string
		data any
	}
	var variants []variant
	reflectNames := []string{ReflectSystem, ReflectDoneRequest, ReflectFinal, ReflectFinalUser}
	for mask := 0; mask < 64; mask++ {
		for _, budget := range []string{"", "low", "MID", "high"} {
			d := &ReflectData{
				SearchMentalModels: mask&1 != 0, Observations: mask&2 != 0, Expand: mask&4 != 0,
				AnswerAsDocument: mask&8 != 0, Recall: mask&16 != 0, ReadMentalModels: mask&32 != 0,
				Budget: budget, MaxTokens: mask,
				Disposition: &domain.Disposition{Literalism: mask % 6},
				Directives:  []domain.Directive{{Rules: []string{"Be brief."}}},
				Retrieved:   []ToolOutput{{Tool: "recall", Output: "{}"}},
			}
			for _, name := range reflectNames {
				variants = append(variants, variant{name, d})
			}
		}
	}
	labels := []domain.EntityLabel{{Key: "k", Type: domain.LabelTypeMultiText}}
	for _, name := range []string{ExtractConcise, ExtractVerbose, ExtractVerbatim, ExtractCustom} {
		variants = append(variants,
			variant{name, &ExtractData{CausalLinks: false, Labels: labels}},
			variant{name, &ExtractData{CausalLinks: true, FreeFormEntities: true, Labels: labels, OptionalDimensions: true}})
	}
	variants = append(variants,
		variant{ExtractUser, &ExtractData{AgentName: "Atlas"}},
		variant{ExtractUser, &ExtractData{AgentName: "Atlas", Context: "support transcript"}})
	for _, c := range []*ConsolidateCapacity{{Max: 3, Remaining: 0}, {Max: 3, Remaining: 1}, {Max: 9, Remaining: 9}} {
		variants = append(variants, variant{ConsolidateUser, &ConsolidateData{Capacity: c, Facts: []SourceFact{{}}}})
	}
	for _, tokens := range []int{100, 850, 1000} {
		d := &DeltaData{DocumentTokens: tokens, DocumentBudget: 1000, Facts: []SourceFact{{ID: "f"}}}
		variants = append(variants, variant{MentalModelStructuredDeltaUser, d})
	}
	variants = append(variants, variant{MentalModelRefreshContext, &RefreshContextData{Name: "n"}})
	for _, v := range variants {
		out, err := Render(v.name, v.data)
		if err != nil {
			t.Fatalf("%s %+v: %v", v.name, v.data, err)
		}
		checkOutput(t, out)
	}
}

// TestSchemasPairWithTemplates checks every response schema is named after a template, so Version of that
// template covers it, and that it fits the strict structured-output subset.
func TestSchemasPairWithTemplates(t *testing.T) {
	entries, err := fs.ReadDir(templatesFS, dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, name := range Names() {
		names[name] = true
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), schemaExt) {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), schemaExt)
		if strings.HasPrefix(stem, "time_parse") {
			continue // owned by another work package, which follows its own schema conventions
		}
		if !names[stem] {
			t.Errorf("%s has no templates/%s.tmpl, so no Version covers it", e.Name(), stem)
			continue
		}
		raw, err := Schema(stem)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		checkStrict(t, e.Name(), "$", schema)
	}
}

// checkStrict walks a schema: every object closes additionalProperties and requires every property.
func checkStrict(t *testing.T, file, at string, node map[string]any) {
	t.Helper()
	if props, ok := node["properties"].(map[string]any); ok {
		if node["additionalProperties"] != false {
			t.Errorf("%s %s: object without additionalProperties: false", file, at)
		}
		var required []string
		if rs, ok := node["required"].([]any); ok {
			for _, r := range rs {
				required = append(required, fmt.Sprint(r))
			}
		}
		keys := make([]string, 0, len(props))
		for k, v := range props {
			keys = append(keys, k)
			if sub, ok := v.(map[string]any); ok {
				checkStrict(t, file, at+"."+k, sub)
			}
		}
		sort.Strings(keys)
		sort.Strings(required)
		if strings.Join(keys, ",") != strings.Join(required, ",") {
			t.Errorf("%s %s: required %v, properties %v", file, at, required, keys)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		checkStrict(t, file, at+"[]", items)
	}
}

// toolSpec is the ai.ToolSpec shape the reflect tool assets use.
type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// TestReflectToolAssets checks the tool list and the done variants decode as tool specs.
func TestReflectToolAssets(t *testing.T) {
	raw, err := JSON(ReflectTools)
	if err != nil {
		t.Fatal(err)
	}
	var tools []toolSpec
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range tools {
		if tool.Description == "" || len(tool.Parameters) == 0 {
			t.Errorf("tool %q lacks a description or parameters", tool.Name)
		}
		got = append(got, tool.Name)
	}
	want := "search_mental_models,read_mental_models,search_observations,recall,expand,done"
	if strings.Join(got, ",") != want {
		t.Errorf("tools = %v, want %s", got, want)
	}
	for _, name := range []string{ReflectToolDoneDirectives, ReflectToolDoneDocument, ReflectToolDoneDocumentDirectives} {
		raw, err := JSON(name)
		if err != nil {
			t.Fatal(err)
		}
		var tool toolSpec
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Name != "done" || tool.Description == "" || len(tool.Parameters) == 0 {
			t.Errorf("%s: %+v", name, tool)
		}
	}
}

// TestEveryJSONAssetIsValid checks every schema and JSON asset parses.
func TestEveryJSONAssetIsValid(t *testing.T) {
	err := fs.WalkDir(templatesFS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, jsonExt) {
			return err
		}
		raw, err := templatesFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		if !json.Valid(raw) {
			t.Errorf("%s is not valid JSON", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestUnknownNames checks unknown templates and assets fail cleanly.
func TestUnknownNames(t *testing.T) {
	if _, err := Render("no_such_template", nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Render err = %v", err)
	}
	if v := Version("no_such_template"); v != "" {
		t.Fatalf("Version = %q", v)
	}
	if _, err := Schema("no_such_template"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Schema err = %v", err)
	}
	if _, err := JSON("no_such_asset"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("JSON err = %v", err)
	}
}

// TestNoticePresent checks the MIT notice ships with the templates.
func TestNoticePresent(t *testing.T) {
	raw, err := templatesFS.ReadFile(path.Join(dir, "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "MIT") && !strings.Contains(string(raw), "Permission is hereby granted") {
		t.Fatal("NOTICE lacks the MIT license text")
	}
}

// extractSystems are the extraction system templates, one per mode.
var extractSystems = []string{ExtractConcise, ExtractVerbose, ExtractVerbatim, ExtractCustom}

// mustRender renders name with data or fails the test.
func mustRender(t *testing.T, name string, data any) string {
	t.Helper()
	out, err := Render(name, data)
	if err != nil {
		t.Fatalf("Render %s: %v", name, err)
	}
	return out
}

// TestExtractFactTypeMatchesHindsight checks every extraction schema offers Hindsight's fact_type labels
// (the retain engine maps "assistant" to experience) and the prompts never ask for "experience".
func TestExtractFactTypeMatchesHindsight(t *testing.T) {
	for _, name := range extractSystems {
		raw, err := Schema(name)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties struct {
				Facts struct {
					Items struct {
						Properties struct {
							FactType struct {
								Enum []string `json:"enum"`
							} `json:"fact_type"`
						} `json:"properties"`
					} `json:"items"`
				} `json:"facts"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties.Facts.Items.Properties.FactType.Enum; strings.Join(got, ",") != "world,assistant" {
			t.Errorf("%s fact_type enum = %v, want [world assistant]", name, got)
		}
		out := mustRender(t, name, &ExtractData{})
		if strings.Contains(out, `"experience"`) || strings.Contains(out, "**experience**") {
			t.Errorf("%s still labels fact_type experience", name)
		}
	}
}

// TestConsolidateSourceMemoriesHaveNoIDs checks the rendered CONSOLIDATE input shows the ids the model may
// copy (new facts, existing observations) and none for an observation's source memories.
func TestConsolidateSourceMemoriesHaveNoIDs(t *testing.T) {
	data, err := decodeFixture(ConsolidateUser)
	if err != nil {
		t.Fatal(err)
	}
	out := mustRender(t, ConsolidateUser, data)
	ids := []string{
		"[c3d4e5f6-a7b8-9012-cdef-123456789012]",
		"[d4e5f6a7-b8c9-0123-defa-234567890123]",
		`"id":"22222222-2222-2222-2222-222222222222"`,
	}
	for _, id := range ids {
		if !strings.Contains(out, id) {
			t.Errorf("output lacks %s", id)
		}
	}
	const memory = `"source_memories":[{"text":"User walks Rex every morning."`
	if strings.Count(out, `"id"`) != 1 || !strings.Contains(out, memory) {
		t.Errorf("source memories must carry no id:\n%s", out)
	}
}

// TestExtractOptionalDimensions checks Hindsight's optional-dimensions switch: off (the default) asks for
// "N/A" and has no OPTIONAL FIELDS section; on asks for null and adds it.
func TestExtractOptionalDimensions(t *testing.T) {
	for _, name := range extractSystems {
		off := mustRender(t, name, &ExtractData{})
		if strings.Contains(off, "OPTIONAL FIELDS") {
			t.Errorf("%s: OPTIONAL FIELDS rendered with the flag off", name)
		}
		on := mustRender(t, name, &ExtractData{OptionalDimensions: true})
		if !strings.Contains(on, "OPTIONAL FIELDS") || strings.Count(on, `"N/A"`) != 1 {
			t.Errorf("%s: flag on must add OPTIONAL FIELDS and mention \"N/A\" only there", name)
		}
		if name != ExtractVerbose && (!strings.Contains(off, `"where": Location if relevant. "N/A" if none.`) ||
			!strings.Contains(on, `"where": Location if relevant. null if none.`)) {
			t.Errorf("%s: the where placeholder does not follow the flag", name)
		}
		checkNullableDimensions(t, name)
	}
}

// checkNullableDimensions checks ExtractSchema follows the flag: plain strings when off (the static schema
// as is), string-or-null when on (Hindsight #4457).
func checkNullableDimensions(t *testing.T, name string) {
	t.Helper()
	static, err := Schema(name)
	if err != nil {
		t.Fatal(err)
	}
	off, err := ExtractSchema(name, false)
	if err != nil || string(off) != string(static) {
		t.Fatalf("%s: ExtractSchema(off) differs from the static schema (%v)", name, err)
	}
	on, err := ExtractSchema(name, true)
	if err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[string]string{"off": "string", "on": "[string null]"} {
		raw := off
		if mode == "on" {
			raw = on
		}
		for dim, typ := range dimensionTypes(t, raw) {
			if typ != want {
				t.Errorf("%s (%s): %s type = %s, want %s", name, mode, dim, typ, want)
			}
		}
	}
}

// dimensionTypes returns the type of each dimension field the extraction schema raw has.
func dimensionTypes(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var schema struct {
		Properties struct {
			Facts struct {
				Items struct {
					Properties map[string]struct {
						Type any `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"facts"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, dim := range dimensionFields {
		if p, ok := schema.Properties.Facts.Items.Properties[dim]; ok {
			out[dim] = fmt.Sprint(p.Type)
		}
	}
	if len(out) < 3 {
		t.Fatalf("schema has only %d dimensions", len(out))
	}
	return out
}

// TestConciseExampleUsesNAMarker checks the concise example's why follows the N/A marker.
func TestConciseExampleUsesNAMarker(t *testing.T) {
	if out := mustRender(t, ExtractConcise, &ExtractData{}); !strings.Contains(out, `why="N/A"`) {
		t.Error(`default concise example must render why="N/A"`)
	}
	if out := mustRender(t, ExtractConcise, &ExtractData{OptionalDimensions: true}); !strings.Contains(out, "why=null") {
		t.Error("optional-dimensions concise example must render why=null")
	}
}

// TestExtractNarrator checks Hindsight's narrator rule: absent without an agent name, and its Context
// clause only when a context is given.
func TestExtractNarrator(t *testing.T) {
	const clause = "BUT the Context above takes precedence"
	if out := mustRender(t, ExtractUser, &ExtractData{}); strings.Contains(out, "Narrator:") {
		t.Errorf("narrator rendered without an agent name:\n%s", out)
	}
	out := mustRender(t, ExtractUser, &ExtractData{AgentName: "Atlas"})
	if !strings.Contains(out, `Narrator: Atlas (the AI agent whose memory this is). By default, first-person `+
		`statements like "I did X" are Atlas's own actions → classify as "assistant".`) || strings.Contains(out, clause) {
		t.Errorf("narrator without context:\n%s", out)
	}
	out = mustRender(t, ExtractUser, &ExtractData{AgentName: "Atlas", Context: "a support call"})
	if !strings.Contains(out, clause) {
		t.Errorf("narrator with context lacks the precedence clause:\n%s", out)
	}
}

// TestExtractCausalSectionGated checks the CAUSAL section renders only with causal links on, and never in
// verbatim mode.
func TestExtractCausalSectionGated(t *testing.T) {
	for _, name := range extractSystems {
		off := mustRender(t, name, &ExtractData{})
		on := mustRender(t, name, &ExtractData{CausalLinks: true})
		if strings.Contains(off, "CAUSAL RELATIONSHIPS") || strings.Contains(off, "caused_by") {
			t.Errorf("%s: causal section rendered with causal links off", name)
		}
		if want := name != ExtractVerbatim; strings.Contains(on, "CAUSAL RELATIONSHIPS") != want {
			t.Errorf("%s: causal section with causal links on = %v, want %v", name, !want, want)
		}
	}
}

// doneToolProps decodes the parameter descriptions of a done tool.
func doneToolProps(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var tool struct {
		Parameters struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	props := map[string]string{}
	for k, v := range tool.Parameters.Properties {
		props[k] = v.Description
	}
	return props
}

// TestDoneToolDirectiveRules checks DoneTool numbers the directive rules into the directive variants'
// answer and directive_compliance descriptions, as Hindsight's _build_done_tool_with_directives does, and
// leaves the other done tools as they are.
func TestDoneToolDirectiveRules(t *testing.T) {
	data, err := decodeFixture(ReflectToolDirectiveRules)
	if err != nil {
		t.Fatal(err)
	}
	rd, ok := data.(*ReflectData)
	if !ok {
		t.Fatalf("fixture decoded to %T", data)
	}
	const rules = "  1. **Tone**: Answer in a formal tone.\n" +
		"  2. **Tone**: Never use emojis.\n" +
		"  3. Cite \"sources\" when <unsure>."
	if got := mustRender(t, ReflectToolDirectiveRules, rd); got != rules {
		t.Fatalf("rules = %q", got)
	}
	for _, name := range []string{ReflectToolDoneDirectives, ReflectToolDoneDocumentDirectives} {
		raw, err := DoneTool(name, rd)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(directiveRulesSlot)) {
			t.Errorf("%s keeps the rules slot", name)
		}
		props := doneToolProps(t, raw)
		if !strings.Contains(props["directive_compliance"], "follows it:\n"+rules+"\n\nFormat:") {
			t.Errorf("%s directive_compliance = %q", name, props["directive_compliance"])
		}
		if a, ok := props["answer"]; ok && !strings.HasSuffix(a, "MUST comply with ALL directives:\n"+rules) {
			t.Errorf("%s answer = %q", name, a)
		}
	}
	plain, err := DoneTool(ReflectToolDoneDocument, rd)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := JSON(ReflectToolDoneDocument); !bytes.Equal(plain, want) {
		t.Error("DoneTool changed a done tool without directive slots")
	}
}

// TestJSONHelperKeepsHTML checks the json template helper does not escape <, > and &.
func TestJSONHelperKeepsHTML(t *testing.T) {
	got, err := toJSON("<a&b>")
	if err != nil || got != `"<a&b>"` {
		t.Fatalf("json = %q, %v", got, err)
	}
}

// TestVersionCoversToolAssets checks Version of a reflect template also hashes the reflect tool files, and
// that a template without tool files hashes just its template and schema.
func TestVersionCoversToolAssets(t *testing.T) {
	base := func(name string) string {
		h := sha256.New()
		for _, file := range []string{name + tmplExt, name + schemaExt} {
			if b, err := templatesFS.ReadFile(path.Join(dir, file)); err == nil {
				h.Write(b)
			}
		}
		return hex.EncodeToString(h.Sum(nil))[:versionLen]
	}
	files := toolFiles(ReflectSystem)
	if len(files) < 5 || !sort.StringsAreSorted(files) {
		t.Fatalf("reflect tool files = %v", files)
	}
	if Version(ReflectSystem) == base(ReflectSystem) {
		t.Error("Version(reflect_system) ignores the reflect tool assets")
	}
	if Version(DedupSystem) != base(DedupSystem) {
		t.Error("Version(dedup_system) changed without tool assets")
	}
}
