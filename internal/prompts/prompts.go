package prompts

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"text/template"

	"memd/internal/domain"
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
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("prompts: json: %w", err)
		}
		return string(b), nil
	},
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

// Version is the content hash of template name (first 16 hex digits of the SHA-256 of its file bytes,
// plus its response schema when it has one). It is recorded on every LLM call so traces and scripted
// fakes can tell prompt revisions apart. An unknown name returns "".
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
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
}

// Schema returns the JSON Schema in templates/<name>.schema.json, the response format of a structured
// call. A missing schema is domain.ErrInvalidArgument.
func Schema(name string) (json.RawMessage, error) {
	return readJSON(name + schemaExt)
}

// JSON returns the JSON asset in templates/<name>.json (for example a tool list whose descriptions are
// prompt text). A missing asset is domain.ErrInvalidArgument.
func JSON(name string) (json.RawMessage, error) {
	return readJSON(name + jsonExt)
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
