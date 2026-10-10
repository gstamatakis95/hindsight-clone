package prompts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memd/internal/domain"
)

// TestEveryTemplateRendersWithFixture renders each template with testdata/<name>.json and fails on a
// template without a fixture, an execution error, empty output or a "<no value>" hole.
func TestEveryTemplateRendersWithFixture(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no templates embedded")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
			if err != nil {
				t.Fatalf("missing fixture testdata/%s.json: %v", name, err)
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			out, err := Render(name, data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatal("empty output")
			}
			if strings.Contains(out, "<no value>") {
				t.Fatalf("output has <no value>:\n%s", out)
			}
			if v := Version(name); len(v) != versionLen {
				t.Fatalf("Version = %q", v)
			}
		})
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
			return err
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
	raw, err := templatesFS.ReadFile(dir + "/NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "MIT") && !strings.Contains(string(raw), "Permission is hereby granted") {
		t.Fatal("NOTICE lacks the MIT license text")
	}
}
