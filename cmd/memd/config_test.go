package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"memd/internal/config"
	"memd/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// baseFile is a valid server config file: every chat step on one model, fast worker timings.
const baseFile = `
defaults:
  models:
    default: {model: m}
worker:
  stale_after: 8s
  heartbeat_interval: 1s
`

// writeConfig writes a server config file into a temporary directory and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// baseEnv is a valid environment over a server config file holding body.
func baseEnv(t *testing.T, body string) map[string]string {
	t.Helper()
	return map[string]string{
		envDSN: "postgres://x", envGatewayURL: "http://gw/v1", envS3Bucket: "memd", envS3PathStyle: "true",
		envS3AccessKey: "ak", envS3SecretKey: "sk", envConfig: writeConfig(t, body),
	}
}

// loadValidate loads args with env and validates the result.
func loadValidate(args []string, env map[string]string) (Config, error) {
	cfg, err := LoadConfig(args, func(k string) string { return env[k] })
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

// TestEffectiveWorkerID: "<MEMD_WORKER_ID or hostname>-<uuid>", unique per process.
func TestEffectiveWorkerID(t *testing.T) {
	host := func() (string, error) { return "node-7", nil }
	noHost := func() (string, error) { return "", errors.New("no hostname") }
	n := 0
	id := func() string { n++; return fmt.Sprintf("u%d", n) }
	cases := []struct {
		configured string
		hostname   func() (string, error)
		want       string
	}{
		{"w", host, "w-u1"},
		{"", host, "node-7-u2"},
		{"", noHost, "worker-u3"},
	}
	for _, c := range cases {
		if got := effectiveWorkerID(c.configured, c.hostname, id); got != c.want {
			t.Errorf("effectiveWorkerID(%q) = %q, want %q", c.configured, got, c.want)
		}
	}
	a := effectiveWorkerID("same", host, uuid.NewString)
	b := effectiveWorkerID("same", host, uuid.NewString)
	if a == b || !strings.HasPrefix(a, "same-") {
		t.Errorf("two processes with MEMD_WORKER_ID=same got %q and %q", a, b)
	}
}

// TestValidateS3Keys: both static S3 keys are required.
func TestValidateS3Keys(t *testing.T) {
	for _, drop := range [][]string{{envS3AccessKey}, {envS3SecretKey}, {envS3AccessKey, envS3SecretKey}} {
		env := baseEnv(t, baseFile)
		for _, k := range drop {
			delete(env, k)
		}
		if _, err := loadValidate([]string{"api"}, env); err == nil || !strings.Contains(err.Error(), envS3AccessKey) {
			t.Errorf("without %v: %v, want the S3 key error", drop, err)
		}
	}
	if _, err := loadValidate([]string{"api"}, baseEnv(t, baseFile)); err != nil {
		t.Fatalf("both keys: %v", err)
	}
}

// TestServerFile checks the server config file: its defaults block is the server level (models included), its
// operator sections reach Config, unset sections take their defaults, and a bad file fails with a message
// naming the key.
func TestServerFile(t *testing.T) {
	cfg, err := loadValidate([]string{"worker"}, baseEnv(t, `
defaults:
  models:
    default: {model: cheap}
    reflect: {model: strong, temperature: 0.2}
    rerank: {model: reranker}
  observations: {reconcile_grace_seconds: 60}
gateway:
  max_concurrent: 8
  timeout: 30s
  slot_concurrency: {extract: 3, embed: 7}
retention: {operations: 24h}
api: {rpc_deadline: 0s}
`))
	if err != nil {
		t.Fatal(err)
	}
	server, err := cfg.File.validate()
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.File
	checks := map[string]bool{
		"default model":   modelOf(server, domain.ModelSlotExtract) == "cheap",
		"slot model":      modelOf(server, domain.ModelSlotReflect) == "strong",
		"rerank":          modelOf(server, domain.ModelSlotRerank) == "reranker",
		"embed default":   server.Models.Embed.Model == config.DefaultEmbedModel,
		"grace":           *server.Observations.ReconcileGraceSeconds == 60,
		"max concurrent":  f.Gateway.MaxConcurrent == 8,
		"timeout":         time.Duration(f.Gateway.Timeout) == 30*time.Second,
		"slot caps":       maps.Equal(f.Gateway.SlotConcurrency, map[domain.ModelSlot]int{"extract": 3, "embed": 7}),
		"retention":       time.Duration(f.Retention.Operations) == 24*time.Hour,
		"event default":   time.Duration(f.Retention.Events) == defaultEventKeep,
		"poll default":    time.Duration(f.Worker.PollInterval) == defaultPollInterval,
		"no rpc deadline": *f.API.RPCDeadline == 0,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("%s: %+v", name, f)
		}
	}
	for name, tc := range map[string]struct{ body, want string }{
		"no models":         {"worker: {poll_interval: 1s}", "defaults.models.default or defaults.models.extract"},
		"only extract":      {"defaults: {models: {extract: {model: x}}}", "defaults.models.reflect"},
		"unknown key":       {baseFile + "gateway: {max_concurent: 3}", "max_concurent"},
		"unknown group key": {"defaults: {retain: {mision: x}}", "mision"},
		"bad duration":      {baseFile + "retention: {events: soon}", "soon"},
		"unknown slot cap":  {baseFile + "gateway: {slot_concurrency: {synthesize: 2}}", "synthesize"},
		"zero slot cap":     {baseFile + "gateway: {slot_concurrency: {extract: 0}}", "extract"},
		"slow heartbeat": {
			"defaults: {models: {default: {model: m}}}\nworker: {stale_after: 8s, heartbeat_interval: 6s}",
			"heartbeat_interval",
		},
		"bad disposition": {
			"defaults: {models: {default: {model: m}}, reflect: {disposition: {empathy: 9}}}",
			"disposition",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadValidate([]string{"worker"}, baseEnv(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// TestExampleConfig: the shipped memd.example.yaml loads, validates and means what its comments say.
func TestExampleConfig(t *testing.T) {
	env := baseEnv(t, "")
	env[envConfig] = filepath.Join("..", "..", "memd.example.yaml")
	cfg, err := loadValidate([]string{"all"}, env)
	if err != nil {
		t.Fatal(err)
	}
	server, err := cfg.File.validate()
	if err != nil {
		t.Fatal(err)
	}
	if modelOf(server, domain.ModelSlotExtract) != "gpt-4o-mini" || modelOf(server, domain.ModelSlotReflect) != "gpt-4o" ||
		modelOf(server, domain.ModelSlotRerank) != "" || *server.Recall.Rerank ||
		server.Retain.ExtractionMode != domain.ExtractionModeConcise ||
		time.Duration(cfg.File.Worker.StaleAfter) != defaultStaleAfter {
		t.Fatalf("example config read as %+v / %+v", server, cfg.File)
	}
}

// TestServerFilePath: the default ./memd.yaml may be absent, an explicit MEMD_CONFIG must exist.
func TestServerFilePath(t *testing.T) {
	t.Chdir(t.TempDir())
	env := baseEnv(t, baseFile)
	delete(env, envConfig)
	if _, err := LoadConfig([]string{"api"}, func(k string) string { return env[k] }); err != nil {
		t.Fatalf("no ./memd.yaml: %v", err)
	}
	if err := os.WriteFile(defaultConfigPath, []byte(baseFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadValidate([]string{"api"}, env); err != nil {
		t.Fatalf("./memd.yaml: %v", err)
	}
	env[envConfig] = filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := LoadConfig([]string{"api"}, func(k string) string { return env[k] }); err == nil {
		t.Fatal("missing explicit config file accepted")
	}
}

// modelOf is the model cfg yields for slot, or "".
func modelOf(cfg domain.BankConfig, slot domain.ModelSlot) string {
	b, _ := cfg.Binding(slot)
	return b.Model
}

// TestLoadConfigEnvAndFlags checks defaults, environment values, flags winning over the environment, and
// the e2e harness's variable names.
func TestLoadConfigEnvAndFlags(t *testing.T) {
	env := baseEnv(t, baseFile)
	cfg, err := LoadConfig([]string{"all", "-grpc-addr", "127.0.0.1:9", "-worker-slots=4"},
		func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{
		"mode":       cfg.Mode == modeAll,
		"dsn":        cfg.DSN == "postgres://x",
		"flag wins":  cfg.GRPCAddr == "127.0.0.1:9",
		"slots flag": cfg.WorkerSlots == 4,
		"path style": cfg.S3PathStyle,
		"file read":  time.Duration(cfg.File.Worker.StaleAfter) == 8*time.Second,
		"region":     cfg.S3Region == defaultS3Region,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("%s: %+v", name, cfg)
		}
	}
}

// TestNormalizeDSN checks the Alembic-style DSN the Makefile exports is accepted.
func TestNormalizeDSN(t *testing.T) {
	cases := map[string]string{
		"postgresql+psycopg://memd@localhost:55432/memd": "postgresql://memd@localhost:55432/memd",
		"postgres://u@h/db?sslmode=disable":              "postgres://u@h/db?sslmode=disable",
		"host=localhost dbname=memd":                     "host=localhost dbname=memd",
	}
	for in, want := range cases {
		if got := normalizeDSN(in); got != want {
			t.Errorf("normalizeDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidateRejects checks each invalid configuration fails before anything connects.
func TestValidateRejects(t *testing.T) {
	cases := map[string]func(env map[string]string) []string{
		"unknown mode": func(map[string]string) []string { return []string{"serve"} },
		"no dsn":       func(env map[string]string) []string { delete(env, envDSN); return []string{"api"} },
		"no gateway":   func(env map[string]string) []string { delete(env, envGatewayURL); return []string{"api"} },
		"no bucket":    func(env map[string]string) []string { delete(env, envS3Bucket); return []string{"worker"} },
		"unknown blob provider": func(env map[string]string) []string {
			env[envBlobProvider] = "gcs"
			return []string{"api"}
		},
		"zero slots": func(env map[string]string) []string { env[envWorkerSlots] = "0"; return []string{"all"} },
		"ingest without a provider": func(map[string]string) []string {
			return []string{"ingest"}
		},
		"unknown ingest provider": func(env map[string]string) []string {
			env[envIngestProvider] = "sqs"
			return []string{"api"}
		},
		"kafka without a dead-letter topic": func(env map[string]string) []string {
			env[envIngestProvider], env[envKafkaBrokers], env[envKafkaTopic] = "kafka", "localhost:9092", "docs"
			return []string{"all"}
		},
		"consumer batch over max_batch_items": func(env map[string]string) []string {
			env[envConfig] = writeConfig(t, baseFile+"ingest: {max_batch_items: 10, consumer: {batch_size: 11}}\n")
			return []string{"worker"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := baseEnv(t, baseFile)
			args := mutate(env)
			cfg, err := LoadConfig(args, func(k string) string { return env[k] })
			if err == nil {
				err = cfg.Validate()
			}
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// embedder is a ai.Embedder returning vectors of a fixed size.
type embedder struct {
	dim int
	err error
}

func (e embedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
	}
	return out, e.err
}

func (e embedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return make([]float32, e.dim), e.err
}

// TestCheckEmbedding checks startup fails unless the gateway returns 768-dimension embeddings.
func TestCheckEmbedding(t *testing.T) {
	if err := checkEmbedding(t.Context(), embedder{dim: domain.EmbeddingDim}); err != nil {
		t.Fatalf("768 dims: %v", err)
	}
	if err := checkEmbedding(t.Context(), embedder{dim: 1536}); err == nil || !strings.Contains(err.Error(), "1536") {
		t.Fatalf("1536 dims: %v", err)
	}
	if err := checkEmbedding(t.Context(), embedder{err: errors.New("down")}); err == nil {
		t.Fatal("gateway down accepted")
	}
}
