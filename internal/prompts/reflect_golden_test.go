package prompts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReflectSystemWithoutRecallGolden renders reflect_system for an observation-scoped refresh (search_observations
// offered, recall withheld) and compares it with testdata/reflect_system_no_recall.golden. The prompt must not
// order the model to call a tool it doesn't have. Regenerate with UPDATE_GOLDEN=1.
func TestReflectSystemWithoutRecallGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "reflect_system_no_recall.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data ReflectData
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&data); err != nil {
		t.Fatal(err)
	}
	out, err := Render(ReflectSystem, &data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "recall") || strings.Contains(out, "RAW FACTS") ||
		strings.Contains(out, "read_mental_models") {
		t.Errorf("prompt names a withheld tool:\n%s", out)
	}
	golden := filepath.Join("testdata", "reflect_system_no_recall.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("reflect_system (no recall) differs from %s; rerun with UPDATE_GOLDEN=1 and review the diff", golden)
	}
}
