package prompts

import (
	"bytes"
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
	MentalModelDeltaSystem:           func() any { return &DeltaData{} },
	MentalModelDeltaUser:             func() any { return &DeltaData{} },
	MentalModelStructuredDeltaSystem: func() any { return &DeltaData{} },
	MentalModelStructuredDeltaUser:   func() any { return &DeltaData{} },
	MentalModelRefreshContext:        func() any { return &RefreshContextData{} },
	"time_parse_system":              func() any { return &map[string]any{} },
	"time_parse_user":                func() any { return &map[string]any{} },
}

// optionalTemplates may be registered before their template exists (owned by another work package).
var optionalTemplates = map[string]bool{"time_parse_system": true, "time_parse_user": true}

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
		if _, isMap := data.(*map[string]any); isMap {
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
	for mask := 0; mask < 16; mask++ {
		for _, budget := range []string{"", "low", "MID", "high"} {
			d := &ReflectData{
				MentalModels: mask&1 != 0, Observations: mask&2 != 0, Expand: mask&4 != 0,
				AnswerAsDocument: mask&8 != 0, Budget: budget, MaxTokens: mask,
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
			variant{name, &ExtractData{CausalLinks: true, FreeFormEntities: true, Labels: labels}})
	}
	for _, c := range []*ConsolidateCapacity{{Max: 3, Remaining: 0}, {Max: 3, Remaining: 1}, {Max: 9, Remaining: 9}} {
		variants = append(variants, variant{ConsolidateUser, &ConsolidateData{Capacity: c, Facts: []SourceFact{{}}}})
	}
	for _, tokens := range []int{100, 850, 1000} {
		d := &DeltaData{DocumentTokens: tokens, DocumentBudget: 1000, Facts: []SourceFact{{ID: "f"}}}
		variants = append(variants, variant{MentalModelDeltaUser, d}, variant{MentalModelStructuredDeltaUser, d})
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

// toolSpec is the port.ToolSpec shape the reflect tool assets use.
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
