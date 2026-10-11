package main

import (
	"memd/eval/harness"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var locomoRecall = harness.RecallOptions{Budget: "high", MaxTokens: 32768, ChunkTokens: 16384}

// TestConfigPrecedence checks built-in < benchmark defaults < file < flags.
func TestConfigPrecedence(t *testing.T) {
	t.Setenv(envJudgeURL, "http://env-gateway/v1")
	t.Setenv(envJudgeModel, "env-judge")
	file := writeYAML(t, `
benchmark: locomo
mode: recall
parallel: 7
judge:
  model: file-judge
barrier:
  poll: 3s
`)
	cfg, _, err := loadConfig([]string{"-config", file, "-parallel", "2", "-category", "2-temporal, 4-single-hop"})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"parallel (flag over file)", cfg.Parallel, 2},
		{"mode (file over benchmark)", cfg.Mode, harness.ModeRecall},
		{"judge model (file over env)", cfg.Judge.Model, "file-judge"},
		{"judge url (env)", cfg.Judge.URL, "http://env-gateway/v1"},
		{"recall (benchmark default)", cfg.Recall, locomoRecall},
		{"poll (file)", cfg.Barrier.Poll, 3 * time.Second},
		{"settle timeout (built-in)", cfg.Barrier.SettleTimeout, 15 * time.Minute},
		{"categories (flag)", strings.Join(cfg.Subset.Categories, "|"), "2-temporal|4-single-hop"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	// A flag naming the benchmark picks its defaults even when the file names another.
	cfg, _, err = loadConfig([]string{"-config", writeYAML(t, "benchmark: locomo\n"), "-benchmark", "golden"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != harness.ModeRecall || cfg.Judge.Kind != "contains" {
		t.Errorf("golden via flag: mode %q judge %q, want golden's recall/contains", cfg.Mode, cfg.Judge.Kind)
	}
}

// TestExampleConfigs keeps the shipped configs in step with Config.
func TestExampleConfigs(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no example configs: %v", err)
	}
	for _, p := range paths {
		cfg, _, err := loadConfig([]string{"-config", p})
		if err == nil {
			err = cfg.validate()
		}
		if err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestConfigRejectsUnknownKeys(t *testing.T) {
	if _, _, err := loadConfig([]string{"-config", writeYAML(t, "benchmark: locomo\nparalel: 2\n")}); err == nil {
		t.Fatal("misspelt key: want error")
	}
}

// TestResumeKeepsRunIdentity checks that a resumed run reads the run's saved config and refuses flags
// that would change what the run measures.
func TestResumeKeepsRunIdentity(t *testing.T) {
	dir := t.TempDir()
	saved := defaultConfig()
	saved.Benchmark, saved.Mode, saved.BankPrefix = "golden", harness.ModeRecall, "eval-1-"
	saved.Judge.Kind = "contains"
	if err := writeConfig(dir, saved); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConfig([]string{"-resume", dir, "-parallel", "9"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BankPrefix != "eval-1-" || cfg.Parallel != 9 {
		t.Errorf("resumed config: bank prefix %q parallel %d", cfg.BankPrefix, cfg.Parallel)
	}
	if _, _, err := loadConfig([]string{"-resume", dir, "-mode", "reflect"}); err == nil {
		t.Error("changing the mode on resume: want error")
	}
}
