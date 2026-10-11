package main

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/eval/harness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runMeta is a run's meta.json: what was run, on which data and models, and when. The full config is the
// run's config.yaml; API keys are in neither.
type runMeta struct {
	// Benchmark is the benchmark's name.
	Benchmark string `json:"benchmark"`
	// Dataset is the data the run loaded.
	Dataset datasetMeta `json:"dataset"`
	// Mode is reflect, recall or rag.
	Mode string `json:"mode"`
	// Commit is the git commit of the checkout the eval ran from (memd itself has no version RPC, so
	// this is memd's commit only when the server was built from the same checkout).
	Commit string `json:"commit,omitempty"`
	// Dirty is true when that checkout had uncommitted changes.
	Dirty bool `json:"dirty,omitempty"`
	// Judge is the judge kind and model.
	Judge modelMeta `json:"judge"`
	// Answer is the rag answer model.
	Answer *modelMeta `json:"answer,omitempty"`
	// Recall is the recall tuning of recall and rag modes.
	Recall harness.RecallOptions `json:"recall"`
	// BankPrefix is the bank id prefix; the banks stay on the server for inspection.
	BankPrefix string `json:"bank_prefix"`
	// Targets describes each target, with the models it reports and its results.
	Targets []targetMeta `json:"targets,omitempty"`
	// StartedAt is when the run was created.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when the run last stopped, finished or not.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	// Resumes records each resume.
	Resumes []resumeMeta `json:"resumes,omitempty"`
	// Complete is true when every question of every target has a result.
	Complete bool `json:"complete"`
}

// datasetMeta identifies the loaded data.
type datasetMeta struct {
	// Path is the file or directory loaded; empty for data built into the binary.
	Path string `json:"path,omitempty"`
	// URL is where the file was downloaded from.
	URL string `json:"url,omitempty"`
	// SHA256 is the hex sha256 of Path.
	SHA256 string `json:"sha256,omitempty"`
}

// modelMeta is an eval-side model.
type modelMeta struct {
	// Kind is the judge kind (llm or contains).
	Kind string `json:"kind,omitempty"`
	// URL is the gateway base URL.
	URL string `json:"url,omitempty"`
	// Model is the model name.
	Model string `json:"model,omitempty"`
}

// targetMeta describes one target and its results.
type targetMeta struct {
	// Name is memd or hindsight.
	Name string `json:"name"`
	// Address is the gRPC address or URL.
	Address string `json:"address"`
	// Tenant is memd's tenant.
	Tenant string `json:"tenant,omitempty"`
	// Models is the model bound to each slot, as the target reports it for the first bank.
	Models map[string]string `json:"models,omitempty"`
	// ModelsError is why Models could not be read.
	ModelsError string `json:"models_error,omitempty"`
	// Accuracy is the overall accuracy.
	Accuracy float64 `json:"accuracy"`
	// Pending counts questions without a result yet.
	Pending int `json:"pending,omitempty"`
	// Usage is token usage per slot.
	Usage []harness.Usage `json:"usage,omitempty"`
}

// resumeMeta records one resume.
type resumeMeta struct {
	// At is when the resume started.
	At time.Time `json:"at"`
	// Commit is the eval checkout's commit at the time.
	Commit string `json:"commit,omitempty"`
	// Dirty is true when the checkout had uncommitted changes.
	Dirty bool `json:"dirty,omitempty"`
}

func newMeta(cfg Config, ds datasetMeta, commit string, dirty bool, now time.Time) *runMeta {
	m := &runMeta{
		Benchmark: cfg.Benchmark, Dataset: ds, Mode: cfg.Mode, Commit: commit, Dirty: dirty,
		Judge:  modelMeta{Kind: cfg.Judge.Kind, URL: cfg.Judge.URL, Model: cfg.Judge.Model},
		Recall: cfg.Recall, BankPrefix: cfg.BankPrefix, StartedAt: now,
	}
	if cfg.Mode == harness.ModeRAG {
		m.Answer = &modelMeta{URL: cfg.answerURL(), Model: cfg.Answer.Model}
	}
	return m
}

// finish records the targets' results and the end time.
func (m *runMeta) finish(rep harness.Report, targets []targetMeta, now time.Time) {
	m.EndedAt = &now
	m.Targets = targets
	m.Complete = len(rep.Targets) > 0
	for i := range m.Targets {
		for _, tr := range rep.Targets {
			if tr.Target != m.Targets[i].Name {
				continue
			}
			m.Targets[i].Accuracy, m.Targets[i].Pending, m.Targets[i].Usage = tr.Overall.Accuracy, tr.Pending, tr.Usage
			m.Complete = m.Complete && tr.Pending == 0
		}
	}
}

func readMeta(dir string) (*runMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, metaFile)) //nolint:gosec // the run directory the user names
	if err != nil {
		return nil, fmt.Errorf("read run metadata: %w", err)
	}
	var m runMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Join(dir, metaFile), err)
	}
	return &m, nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// gitCommit returns the commit of the checkout in the working directory and whether it has uncommitted
// changes; "" when git or the checkout is not available.
func gitCommit(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false
	}
	status, err := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=no").Output()
	return strings.TrimSpace(string(head)), err == nil && len(strings.TrimSpace(string(status))) > 0
}
