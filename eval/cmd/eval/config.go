package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"memd/eval/benchmark"
	"memd/eval/harness"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment variables the eval reads. API keys come only from the environment and are never written
// to the run directory.
const (
	envJudgeURL     = "MEMD_EVAL_JUDGE_URL"
	envJudgeModel   = "MEMD_EVAL_JUDGE_MODEL"
	envJudgeKey     = "MEMD_EVAL_JUDGE_API_KEY" //nolint:gosec // an environment variable name, not a credential
	envAnswerURL    = "MEMD_EVAL_ANSWER_URL"
	envAnswerModel  = "MEMD_EVAL_ANSWER_MODEL"
	envAnswerKey    = "MEMD_EVAL_ANSWER_API_KEY"    //nolint:gosec // an environment variable name, not a credential
	envHindsightKey = "MEMD_EVAL_HINDSIGHT_API_KEY" //nolint:gosec // an environment variable name, not a credential
)

// configFile is the resolved config saved in every run directory; -resume reads it back.
const configFile = "config.yaml"

// Config is one run's settings: the YAML run config file, with flags of the same meaning on top.
// Precedence, lowest first: built-in defaults (and MEMD_EVAL_* variables), the benchmark's defaults, the
// file, the flags.
type Config struct {
	// Benchmark is the registered benchmark to run (eval list shows them).
	Benchmark string `yaml:"benchmark"`
	// Data is a local dataset file (or fixture directory) to use instead of the download.
	Data string `yaml:"data,omitempty"`
	// DataDir is the download cache; empty = ~/.cache/memd-eval.
	DataDir string `yaml:"data_dir,omitempty"`
	// Mode is reflect, recall or rag.
	Mode string `yaml:"mode"`
	// OutDir is where run directories are created.
	OutDir string `yaml:"out_dir"`
	// BankPrefix is the bank id prefix; empty = eval-<unix time>-, so each run gets fresh banks.
	BankPrefix string `yaml:"bank_prefix,omitempty"`
	// Parallel is how many conversations run at once.
	Parallel int `yaml:"parallel"`
	// CallTimeout bounds each target, answer or judge call; 0 = none.
	CallTimeout time.Duration `yaml:"call_timeout"`
	// Memd is the memd target.
	Memd MemdConfig `yaml:"memd"`
	// Hindsight is the optional stock Hindsight target.
	Hindsight HindsightConfig `yaml:"hindsight,omitempty"`
	// Judge grades the answers.
	Judge JudgeConfig `yaml:"judge"`
	// Answer is the answer model of rag mode.
	Answer ModelConfig `yaml:"answer,omitempty"`
	// Subset narrows the dataset for a quicker run.
	Subset SubsetConfig `yaml:"subset,omitempty"`
	// Barrier tunes the wait for background work after retain.
	Barrier BarrierConfig `yaml:"barrier"`
	// Recall tunes Recall in recall and rag modes.
	Recall harness.RecallOptions `yaml:"recall,omitempty"`
}

// MemdConfig is the memd target.
type MemdConfig struct {
	// Addr is memd's gRPC address.
	Addr string `yaml:"addr"`
	// Tenant is sent as x-tenant-id.
	Tenant string `yaml:"tenant"`
	// Stub runs memd's side against the in-process stub server instead of Addr.
	Stub bool `yaml:"stub,omitempty"`
	// Skip leaves memd out (with a Hindsight URL, to run Hindsight alone).
	Skip bool `yaml:"skip,omitempty"`
}

// HindsightConfig is the stock Hindsight target; its API key comes from MEMD_EVAL_HINDSIGHT_API_KEY.
type HindsightConfig struct {
	// URL is Hindsight's HTTP API; empty = not run.
	URL string `yaml:"url,omitempty"`
}

// JudgeConfig selects the judge. The API key comes from MEMD_EVAL_JUDGE_API_KEY.
type JudgeConfig struct {
	// Kind is llm or contains.
	Kind string `yaml:"kind"`
	// URL is the OpenAI-compatible gateway base URL (for example http://gateway:4000/v1).
	URL string `yaml:"url,omitempty"`
	// Model is the judge model.
	Model string `yaml:"model,omitempty"`
}

// ModelConfig is the rag answer model. The API key comes from MEMD_EVAL_ANSWER_API_KEY, else the judge's.
type ModelConfig struct {
	// URL is the gateway base URL; empty = the judge's.
	URL string `yaml:"url,omitempty"`
	// Model is the answer model.
	Model string `yaml:"model,omitempty"`
}

// SubsetConfig narrows the dataset. Zero fields keep everything.
type SubsetConfig struct {
	// Categories keeps only these question categories.
	Categories []string `yaml:"categories,omitempty"`
	// Conversations keeps the first n conversations.
	Conversations int `yaml:"conversations,omitempty"`
	// Questions keeps the first n questions of each conversation.
	Questions int `yaml:"questions,omitempty"`
}

// BarrierConfig tunes the wait for a bank's background work.
type BarrierConfig struct {
	// Poll is the interval between pending-operation polls.
	Poll time.Duration `yaml:"poll"`
	// QuietPolls is how many consecutive idle polls end the wait.
	QuietPolls int `yaml:"quiet_polls"`
	// SettleTimeout bounds the wait per conversation.
	SettleTimeout time.Duration `yaml:"settle_timeout"`
}

// defaultConfig is the built-in layer, with the MEMD_EVAL_* model settings.
func defaultConfig() Config {
	return Config{
		Mode: harness.ModeReflect, OutDir: "eval-results", Parallel: 4, CallTimeout: 5 * time.Minute,
		Memd:    MemdConfig{Addr: "localhost:50051", Tenant: "eval"},
		Judge:   JudgeConfig{Kind: "llm", URL: os.Getenv(envJudgeURL), Model: os.Getenv(envJudgeModel)},
		Answer:  ModelConfig{URL: os.Getenv(envAnswerURL), Model: os.Getenv(envAnswerModel)},
		Barrier: BarrierConfig{Poll: time.Second, QuietPolls: 2, SettleTimeout: 15 * time.Minute},
	}
}

// applyBenchmark overlays b's defaults.
func (c *Config) applyBenchmark(d benchmark.Defaults) {
	if d.Mode != "" {
		c.Mode = d.Mode
	}
	if d.Judge != "" {
		c.Judge.Kind = d.Judge
	}
	if d.Recall != (harness.RecallOptions{}) {
		c.Recall = d.Recall
	}
	if d.PollInterval > 0 {
		c.Barrier.Poll = d.PollInterval
	}
	if d.SettleTimeout > 0 {
		c.Barrier.SettleTimeout = d.SettleTimeout
	}
}

// listFlag is a comma-separated list flag.
type listFlag struct{ p *[]string }

func (l listFlag) String() string {
	if l.p == nil {
		return ""
	}
	return strings.Join(*l.p, ",")
}

func (l listFlag) Set(s string) error {
	*l.p = nil
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l.p = append(*l.p, part)
		}
	}
	return nil
}

// runFlags are the flags that are not config fields.
type runFlags struct {
	config, resume string
}

// bindFlags defines the run flags on fs, writing into c and rf; each flag's default is c's current value.
func bindFlags(fs *flag.FlagSet, c *Config, rf *runFlags) {
	fs.StringVar(&rf.config, "config", "", "YAML run config file (flags override it)")
	fs.StringVar(&rf.resume, "resume", "", "resume the run in this directory (its config.yaml is the config file)")
	fs.StringVar(&c.Benchmark, "benchmark", c.Benchmark, "benchmark to run (eval list)")
	fs.StringVar(&c.Data, "data", c.Data, "local dataset file or fixture directory instead of the download")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "download cache (default ~/.cache/memd-eval)")
	fs.StringVar(&c.Mode, "mode", c.Mode, "reflect | recall | rag")
	fs.StringVar(&c.OutDir, "out-dir", c.OutDir, "directory run directories are created in")
	fs.StringVar(&c.BankPrefix, "bank-prefix", c.BankPrefix, "bank id prefix (default eval-<unix time>-)")
	fs.IntVar(&c.Parallel, "parallel", c.Parallel, "conversations run concurrently")
	fs.DurationVar(&c.CallTimeout, "call-timeout", c.CallTimeout, "timeout of each target, answer or judge call")
	fs.StringVar(&c.Memd.Addr, "addr", c.Memd.Addr, "memd gRPC address")
	fs.StringVar(&c.Memd.Tenant, "tenant", c.Memd.Tenant, "tenant sent as x-tenant-id")
	fs.BoolVar(&c.Memd.Stub, "stub", c.Memd.Stub, "run memd's side against an in-process stub server")
	fs.BoolVar(&c.Memd.Skip, "skip-memd", c.Memd.Skip, "do not run against memd (use with -hindsight-url)")
	fs.StringVar(&c.Hindsight.URL, "hindsight-url", c.Hindsight.URL, "also run against stock Hindsight at this URL")
	fs.StringVar(&c.Judge.Kind, "judge", c.Judge.Kind, "llm | contains")
	fs.StringVar(&c.Judge.URL, "judge-url", c.Judge.URL, "judge gateway base URL ("+envJudgeURL+")")
	fs.StringVar(&c.Judge.Model, "judge-model", c.Judge.Model, "judge model ("+envJudgeModel+")")
	fs.StringVar(&c.Answer.URL, "answer-url", c.Answer.URL, "rag answer gateway base URL (default: the judge's)")
	fs.StringVar(&c.Answer.Model, "answer-model", c.Answer.Model, "rag answer model ("+envAnswerModel+")")
	fs.Var(listFlag{&c.Subset.Categories}, "category", "comma-separated categories to keep")
	fs.IntVar(&c.Subset.Conversations, "limit", c.Subset.Conversations, "max conversations (0 = all)")
	fs.IntVar(&c.Subset.Questions, "questions", c.Subset.Questions, "max questions per conversation (0 = all)")
	fs.DurationVar(&c.Barrier.Poll, "poll", c.Barrier.Poll, "barrier poll interval")
	fs.IntVar(&c.Barrier.QuietPolls, "quiet-polls", c.Barrier.QuietPolls, "consecutive idle polls that end the barrier")
	fs.DurationVar(&c.Barrier.SettleTimeout, "settle-timeout", c.Barrier.SettleTimeout, "barrier timeout per conversation")
	fs.StringVar(&c.Recall.Budget, "recall-budget", c.Recall.Budget, "recall budget: low | mid | high")
	fs.IntVar(&c.Recall.MaxTokens, "recall-max-tokens", c.Recall.MaxTokens, "recall result token budget")
	fs.IntVar(&c.Recall.ChunkTokens, "recall-chunk-tokens", c.Recall.ChunkTokens, "source chunk token budget (0 = none)")
}

// loadConfig resolves the run config from args. It parses the flags twice: once to find the config file
// and the benchmark (whose defaults sit below the file), and once more on top of the file.
func loadConfig(args []string) (Config, runFlags, error) {
	var scratch Config
	var rf runFlags
	first := flag.NewFlagSet("eval run", flag.ContinueOnError)
	bindFlags(first, &scratch, &rf)
	if err := first.Parse(args); err != nil {
		return Config{}, rf, fmt.Errorf("flags: %w", err)
	}
	if first.NArg() > 0 {
		return Config{}, rf, fmt.Errorf("unexpected argument %q", first.Arg(0))
	}
	file := rf.config
	if rf.resume != "" {
		if file != "" {
			return Config{}, rf, errors.New("-resume uses the run's own config.yaml; drop -config")
		}
		file = filepath.Join(rf.resume, configFile)
	}
	var raw []byte
	var fromFile Config
	if file != "" {
		var err error
		if raw, err = os.ReadFile(filepath.Clean(file)); err != nil {
			return Config{}, rf, fmt.Errorf("read config: %w", err)
		}
		if err := decodeYAML(raw, &fromFile); err != nil {
			return Config{}, rf, fmt.Errorf("config %s: %w", file, err)
		}
	}
	cfg := defaultConfig()
	name := scratch.Benchmark
	if name == "" {
		name = fromFile.Benchmark
	}
	if b, ok := benchmark.Get(name); ok {
		cfg.applyBenchmark(b.Defaults())
	}
	if raw != nil {
		if err := decodeYAML(raw, &cfg); err != nil {
			return Config{}, rf, fmt.Errorf("config %s: %w", file, err)
		}
	}
	second := flag.NewFlagSet("eval run", flag.ContinueOnError)
	bindFlags(second, &cfg, &rf)
	if err := second.Parse(args); err != nil {
		return Config{}, rf, fmt.Errorf("flags: %w", err)
	}
	if rf.resume != "" {
		if err := cfg.sameRun(fromFile); err != nil {
			return Config{}, rf, err
		}
	}
	return cfg, rf, nil
}

// decodeYAML decodes raw into c strictly (unknown keys are errors); keys absent from raw keep c's values.
func decodeYAML(raw []byte, c *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// validate checks the settings that would otherwise fail deep into a run.
func (c *Config) validate() error {
	if _, ok := benchmark.Get(c.Benchmark); !ok {
		return fmt.Errorf("unknown benchmark %q (want one of %s)", c.Benchmark, strings.Join(benchmarkNames(), ", "))
	}
	switch {
	case c.Mode != harness.ModeReflect && c.Mode != harness.ModeRecall && c.Mode != harness.ModeRAG:
		return fmt.Errorf("unknown mode %q (want %s)", c.Mode, strings.Join(harness.Modes, ", "))
	case c.Memd.Skip && c.Hindsight.URL == "":
		return errors.New("no target: memd.skip needs hindsight.url")
	}
	return c.validateModels()
}

// validateModels checks the judge and, in rag mode, the answer model.
func (c *Config) validateModels() error {
	switch {
	case c.Judge.Kind != "llm" && c.Judge.Kind != "contains":
		return fmt.Errorf("unknown judge %q (want llm or contains)", c.Judge.Kind)
	case c.Judge.Kind == "llm" && (c.Judge.URL == "" || c.Judge.Model == ""):
		return fmt.Errorf("the llm judge needs judge.url and judge.model (-judge-url, -judge-model, %s, %s)",
			envJudgeURL, envJudgeModel)
	case c.Mode == harness.ModeRAG && c.Answer.Model == "":
		return fmt.Errorf("rag mode needs answer.model (-answer-model or %s)", envAnswerModel)
	case c.Mode == harness.ModeRAG && c.answerURL() == "":
		return errors.New("rag mode needs answer.url or judge.url")
	}
	return nil
}

// answerURL is the answer model's gateway: its own URL, else the judge's.
func (c *Config) answerURL() string {
	if c.Answer.URL != "" {
		return c.Answer.URL
	}
	return c.Judge.URL
}

// sameRun fails when c changes a setting that a resumed run must keep, compared with the run's saved
// config.
func (c *Config) sameRun(saved Config) error {
	checks := []struct{ name, got, want string }{
		{"benchmark", c.Benchmark, saved.Benchmark},
		{"mode", c.Mode, saved.Mode},
		{"bank_prefix", c.BankPrefix, saved.BankPrefix},
		{"judge.kind", c.Judge.Kind, saved.Judge.Kind},
		{"judge.model", c.Judge.Model, saved.Judge.Model},
		{"answer.model", c.Answer.Model, saved.Answer.Model},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			return fmt.Errorf("resume: %s is %q in the run, cannot change it to %q", ch.name, ch.want, ch.got)
		}
	}
	return nil
}

// writeConfig saves c as the run's config.yaml.
func writeConfig(dir string, c Config) error {
	var raw bytes.Buffer
	enc := yaml.NewEncoder(&raw)
	enc.SetIndent(2)
	if err := errors.Join(enc.Encode(c), enc.Close()); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), raw.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// benchmarkNames lists the registered benchmarks.
func benchmarkNames() []string {
	var names []string
	for _, b := range benchmark.All() {
		names = append(names, b.Name())
	}
	return names
}
