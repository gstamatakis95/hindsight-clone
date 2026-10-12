package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"memd/eval/benchmark"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/stub"
	"memd/eval/target"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Files of a run directory besides the journal (harness.ProgressFile, harness.ResultsFile) and
// config.yaml.
const (
	reportJSON = "report.json"
	reportMD   = "report.md"
	metaFile   = "meta.json"
)

// runCmd executes `eval run`: resolve the config and dataset, run every target into the run directory's
// journal, then write the reports and metadata.
func runCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, rf, err := loadConfig(args)
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	b, _ := benchmark.Get(cfg.Benchmark)
	ds, err := resolveData(ctx, cfg, b)
	if err != nil {
		return err
	}
	convs, err := b.Load(ds.Path)
	if err != nil {
		return fmt.Errorf("load %s: %w", cfg.Benchmark, err)
	}
	convs = harness.Filter{
		Categories: cfg.Subset.Categories, MaxConversations: cfg.Subset.Conversations, MaxQuestions: cfg.Subset.Questions,
	}.Apply(convs)
	if len(convs) == 0 {
		return errors.New("no questions left after the subset filter")
	}
	dir, meta, err := prepareRunDir(ctx, &cfg, rf.resume, ds, time.Now().UTC())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "eval: %s, %d conversations, run directory %s\n", cfg.Benchmark, len(convs), dir)
	rep, targets, runErr := runTargets(ctx, cfg, dir, convs)
	meta.finish(rep, targets, time.Now().UTC())
	if err := writeOutputs(dir, rep, meta, stdout); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil {
		return fmt.Errorf("%w (resume with: eval run -resume %s)", runErr, dir)
	}
	return gate(b, rep)
}

// resolveData finds the dataset: the -data path, else the benchmark's download (fetched into the cache
// when needed), else nothing for a benchmark that brings its own data.
func resolveData(ctx context.Context, cfg Config, b benchmark.Benchmark) (datasetMeta, error) {
	if cfg.Data != "" {
		sum, err := benchmark.HashPath(cfg.Data)
		if err != nil {
			return datasetMeta{}, fmt.Errorf("dataset: %w", err)
		}
		return datasetMeta{Path: cfg.Data, SHA256: sum}, nil
	}
	f := b.Dataset()
	if f.URL == "" {
		return datasetMeta{}, nil
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = benchmark.DefaultDataDir()
	}
	path, err := benchmark.Fetch(ctx, http.DefaultClient, dataDir, b)
	if err != nil {
		return datasetMeta{}, fmt.Errorf("dataset: %w", err)
	}
	return datasetMeta{Path: path, URL: f.URL, SHA256: f.SHA256}, nil
}

// prepareRunDir creates a new run directory with its config.yaml and metadata, or, when resuming, reopens
// the run's metadata and checks the dataset is the same.
func prepareRunDir(
	ctx context.Context, cfg *Config, resume string, ds datasetMeta, now time.Time,
) (string, *runMeta, error) {
	commit, dirty := gitCommit(ctx)
	if resume != "" {
		m, err := readMeta(resume)
		if err != nil {
			return "", nil, err
		}
		if m.Dataset.SHA256 != ds.SHA256 {
			return "", nil, fmt.Errorf("resume: dataset sha256 is %s, the run used %s", ds.SHA256, m.Dataset.SHA256)
		}
		m.Resumes = append(m.Resumes, resumeMeta{At: now, Commit: commit, Dirty: dirty})
		return resume, m, writeJSON(filepath.Join(resume, metaFile), m)
	}
	if cfg.BankPrefix == "" {
		cfg.BankPrefix = fmt.Sprintf("eval-%d-", now.Unix())
	}
	if err := os.MkdirAll(cfg.OutDir, 0o750); err != nil {
		return "", nil, fmt.Errorf("create %s: %w", cfg.OutDir, err)
	}
	dir := filepath.Join(cfg.OutDir, cfg.Benchmark+"-"+now.Format("20060102-150405"))
	if err := os.Mkdir(dir, 0o750); err != nil {
		return "", nil, fmt.Errorf("create run directory: %w", err)
	}
	if err := writeConfig(dir, *cfg); err != nil {
		return "", nil, err
	}
	m := newMeta(*cfg, ds, commit, dirty, now)
	return dir, m, writeJSON(filepath.Join(dir, metaFile), m)
}

// runTargets runs every target in turn over convs, journaling into dir, and returns the report and what
// the metadata records about each target. It stops at the first interrupted target.
func runTargets(
	ctx context.Context, cfg Config, dir string, convs []harness.Conversation,
) (harness.Report, []targetMeta, error) {
	rep := harness.Report{Benchmark: cfg.Benchmark, Mode: cfg.Mode}
	journal, err := harness.OpenJournal(dir)
	if err != nil {
		return rep, nil, fmt.Errorf("open journal: %w", err)
	}
	defer func() { _ = journal.Close() }()
	j, a := newJudge(cfg)
	targets, closeAll, err := newTargets(cfg)
	if err != nil {
		return rep, nil, err
	}
	defer closeAll()
	rcfg := harness.Config{
		Mode: cfg.Mode, BankPrefix: cfg.BankPrefix, Parallel: cfg.Parallel, PollInterval: cfg.Barrier.Poll,
		QuietPolls: cfg.Barrier.QuietPolls, SettleTimeout: cfg.Barrier.SettleTimeout, CallTimeout: cfg.CallTimeout,
		Recall: cfg.Recall,
	}
	var metas []targetMeta
	for _, t := range targets {
		r, err := harness.NewRunner(harness.Deps{Target: t.Target, Judge: j, Answerer: a, Journal: journal}, rcfg)
		if err != nil {
			return rep, metas, fmt.Errorf("%s: %w", t.meta.Name, err)
		}
		tr, err := r.Run(ctx, convs)
		rep.Targets = append(rep.Targets, tr)
		reportModels(ctx, t, harness.BankID(cfg.BankPrefix, convs[0].ID))
		metas = append(metas, t.meta)
		if err != nil {
			return rep, metas, fmt.Errorf("%s: %w", t.meta.Name, err)
		}
	}
	return rep, metas, nil
}

// newJudge builds the judge and, in rag mode, the answerer.
func newJudge(cfg Config) (harness.Judge, harness.Answerer) {
	var j harness.Judge = judge.Contains{}
	judgeKey := os.Getenv(envJudgeKey)
	if cfg.Judge.Kind == "llm" {
		j = judge.NewLLM(judge.NewChat(cfg.Judge.URL, cfg.Judge.Model, judgeKey, nil))
	}
	if cfg.Mode != harness.ModeRAG {
		return j, nil
	}
	key := os.Getenv(envAnswerKey)
	if key == "" {
		key = judgeKey
	}
	return j, judge.NewAnswerer(judge.NewChat(cfg.answerURL(), cfg.Answer.Model, key, nil))
}

// runTarget is a target with what the metadata records about it.
type runTarget struct {
	harness.Target
	meta targetMeta
}

// newTargets connects to memd (or starts the stub) and Hindsight as configured.
func newTargets(cfg Config) ([]*runTarget, func(), error) {
	var (
		targets []*runTarget
		closers []func()
	)
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	if !cfg.Memd.Skip {
		addr := cfg.Memd.Addr
		if cfg.Memd.Stub {
			stubAddr, stopStub, err := stub.Start(stub.Options{PendingPolls: 2})
			if err != nil {
				return nil, closeAll, fmt.Errorf("start stub: %w", err)
			}
			addr = stubAddr
			closers = append(closers, stopStub)
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("dial memd %s: %w", addr, err)
		}
		closers = append(closers, func() { _ = conn.Close() })
		label := addr
		if cfg.Memd.Stub {
			label = "in-process stub"
		}
		targets = append(targets, &runTarget{
			Target: target.NewMemd(conn, cfg.Memd.Tenant, cfg.Memd.Org),
			meta:   targetMeta{Name: "memd", Address: label, Tenant: cfg.Memd.Tenant, Org: cfg.Memd.Org},
		})
	}
	if cfg.Hindsight.URL != "" {
		targets = append(targets, &runTarget{
			Target: target.NewHindsight(cfg.Hindsight.URL, os.Getenv(envHindsightKey), nil),
			meta:   targetMeta{Name: "hindsight", Address: cfg.Hindsight.URL},
		})
	}
	return targets, closeAll, nil
}

// reportModels records the models the target says are bound to its slots, read from bank. It runs even
// after the run was interrupted, so it does not inherit ctx's cancellation.
func reportModels(ctx context.Context, t *runTarget, bank string) {
	mr, ok := t.Target.(harness.ModelReporter)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	models, err := mr.Models(ctx, bank)
	if err != nil {
		t.meta.ModelsError = err.Error()
		return
	}
	t.meta.Models = models
}

// writeOutputs writes report.json (without the per-question rows, which are in results.jsonl), report.md
// and meta.json, and prints the Markdown report.
func writeOutputs(dir string, rep harness.Report, m *runMeta, stdout io.Writer) error {
	if err := rep.WriteMarkdown(stdout); err != nil {
		return fmt.Errorf("print report: %w", err)
	}
	slim := rep
	slim.Targets = make([]harness.TargetReport, len(rep.Targets))
	for i, t := range rep.Targets {
		t.Questions = nil
		slim.Targets[i] = t
	}
	return errors.Join(
		writeFile(filepath.Join(dir, reportJSON), slim.WriteJSON),
		writeFile(filepath.Join(dir, reportMD), rep.WriteMarkdown),
		writeJSON(filepath.Join(dir, metaFile), m),
	)
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// gate applies the benchmark's gate, if it has one, to every target.
func gate(b benchmark.Benchmark, rep harness.Report) error {
	g, ok := b.(benchmark.Gater)
	if !ok {
		return nil
	}
	var errs []error
	for _, t := range rep.Targets {
		errs = append(errs, g.Gate(t))
	}
	return errors.Join(errs...)
}
