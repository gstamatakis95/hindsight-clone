package prompts

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"memd/internal/domain"
	"path"
	"sort"
	"strings"
	"sync"
	"text/template"
)

// templatesFS holds every prompt template (<name>.tmpl), response schema (<name>.schema.json) and JSON
// asset (<name>.json, e.g. tool declarations) plus the MIT NOTICE of the Hindsight prompts they port.
//
//go:embed templates
var templatesFS embed.FS

const (
	dir        = "templates"
	tmplExt    = ".tmpl"
	schemaExt  = ".schema.json"
	jsonExt    = ".json"
	versionLen = 16
)

var (
	parseOnce sync.Once
	parsed    map[string]*template.Template
	parseErr  error
)

// funcs are the helpers every template may use.
var funcs = template.FuncMap{
	"join":  strings.Join,
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
	"add":   func(a, b int) int { return a + b },
	"json":  toJSON,
}

// toJSON is the json helper: compact JSON without HTML escaping, so <, > and & reach the model as written.
func toJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("prompts: json: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// load parses every template once; a template that fails to parse fails every Render.
func load() (map[string]*template.Template, error) {
	parseOnce.Do(func() {
		parsed = map[string]*template.Template{}
		for _, name := range Names() {
			raw, err := templatesFS.ReadFile(path.Join(dir, name+tmplExt))
			if err != nil {
				parseErr = fmt.Errorf("prompts: read %s: %w", name, err)
				return
			}
			t, err := template.New(name).Funcs(funcs).Option("missingkey=error").Parse(string(raw))
			if err != nil {
				parseErr = fmt.Errorf("prompts: parse %s: %w", name, err)
				return
			}
			parsed[name] = t
		}
	})
	return parsed, parseErr
}

// Names lists every template name (the file stem of templates/<name>.tmpl), sorted.
func Names() []string {
	entries, err := fs.ReadDir(templatesFS, dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), tmplExt) {
			names = append(names, strings.TrimSuffix(e.Name(), tmplExt))
		}
	}
	sort.Strings(names)
	return names
}

// Render executes template name with data. An unknown name is domain.ErrInvalidArgument; a missing map
// key or a failing helper is an execution error.
func Render(name string, data any) (string, error) {
	ts, err := load()
	if err != nil {
		return "", err
	}
	t, ok := ts[name]
	if !ok {
		return "", fmt.Errorf("%w: unknown prompt template %q", domain.ErrInvalidArgument, name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompts: render %s: %w", name, err)
	}
	return buf.String(), nil
}

// Version is the content hash of template name: the first 16 hex digits of the SHA-256 of its file
// bytes, then its response schema when it has one, then every tool file of its operation
// (templates/<op>_tool*.json and <op>_tool*.tmpl in name order, where <op> is the name up to the first
// underscore, e.g. reflect_tools.json for reflect_system), since tool descriptions are prompt text too.
// It is recorded on every LLM call so traces and scripted fakes can tell prompt revisions apart. An
// unknown name returns "".
func Version(name string) string {
	raw, err := templatesFS.ReadFile(path.Join(dir, name+tmplExt))
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write(raw)
	if schema, err := templatesFS.ReadFile(path.Join(dir, name+schemaExt)); err == nil {
		h.Write(schema)
	}
	for _, file := range toolFiles(name) {
		if b, err := templatesFS.ReadFile(file); err == nil {
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
}

// toolFiles lists the tool declarations and tool templates of name's operation, sorted.
func toolFiles(name string) []string {
	op, _, _ := strings.Cut(name, "_")
	matches, err := fs.Glob(templatesFS, path.Join(dir, op+"_tool*"))
	if err != nil {
		return nil
	}
	var files []string
	for _, m := range matches {
		if strings.HasSuffix(m, jsonExt) || strings.HasSuffix(m, tmplExt) {
			files = append(files, m)
		}
	}
	sort.Strings(files)
	return files
}

// Schema returns the JSON Schema in templates/<name>.schema.json, the response format of a structured
// call. A missing schema is domain.ErrInvalidArgument.
func Schema(name string) (json.RawMessage, error) {
	return readJSON(name + schemaExt)
}

// dimensionFields are the extraction dimensions Hindsight's optional-dimensions mode makes nullable.
var dimensionFields = []string{"when", "where", "who", "why"}

// ExtractSchema returns the response schema of extraction template name for ExtractData.OptionalDimensions.
// Off (the default), when / where / who / why are plain strings, as the static schema says; on, each
// present dimension also accepts null and its description says null instead of 'N/A' (Hindsight #4457).
func ExtractSchema(name string, optionalDimensions bool) (json.RawMessage, error) {
	raw, err := Schema(name)
	if err != nil || !optionalDimensions {
		return raw, err
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("prompts: %s schema: %w", name, err)
	}
	props, ok := dig(schema, "properties", "facts", "items", "properties")
	if !ok {
		return nil, fmt.Errorf("%w: %s schema has no facts.items.properties", domain.ErrInvalidArgument, name)
	}
	for _, dim := range dimensionFields {
		field, ok := props[dim].(map[string]any)
		if !ok {
			continue
		}
		field["type"] = []any{"string", "null"}
		if desc, ok := field["description"].(string); ok {
			field["description"] = strings.ReplaceAll(desc, "'N/A'", "null")
		}
	}
	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("prompts: %s schema: %w", name, err)
	}
	return out, nil
}

// dig walks nested JSON objects by key.
func dig(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, true
}

// JSON returns the JSON asset in templates/<name>.json (for example a tool list whose descriptions are
// prompt text). A missing asset is domain.ErrInvalidArgument.
func JSON(name string) (json.RawMessage, error) {
	return readJSON(name + jsonExt)
}

// directiveRulesSlot is where a directive-aware done tool's descriptions take the numbered rule list.
const directiveRulesSlot = "{directive_rules}"

// DoneTool returns the done-tool asset name (one of the ReflectToolDone* names) ready to send: like
// Hindsight's _build_done_tool_with_directives, the directive variants get the active directive rules
// (ReflectToolDirectiveRules rendered with data) numbered into their answer and directive_compliance
// descriptions. Assets without that slot are returned as JSON(name) returns them. Pick a directive
// variant only when some directive has rules (the predicate reflect_system's DIRECTIVES block uses);
// otherwise the list is empty.
func DoneTool(name string, data *ReflectData) (json.RawMessage, error) {
	raw, err := JSON(name)
	if err != nil || !bytes.Contains(raw, []byte(directiveRulesSlot)) {
		return raw, err
	}
	rules, err := Render(ReflectToolDirectiveRules, data)
	if err != nil {
		return nil, err
	}
	quoted, err := toJSON(rules)
	if err != nil {
		return nil, err
	}
	out := bytes.ReplaceAll(raw, []byte(directiveRulesSlot), []byte(quoted[1:len(quoted)-1]))
	if !json.Valid(out) {
		return nil, fmt.Errorf("prompts: done tool %s is not valid JSON after inserting the rules", name)
	}
	return json.RawMessage(out), nil
}

func readJSON(file string) (json.RawMessage, error) {
	raw, err := templatesFS.ReadFile(path.Join(dir, file))
	if err != nil {
		return nil, fmt.Errorf("%w: unknown prompt asset %q", domain.ErrInvalidArgument, file)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("prompts: %s is not valid JSON", file)
	}
	return json.RawMessage(raw), nil
}
