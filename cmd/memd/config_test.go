package main

import (
	"bytes"
	"context"
	"errors"
	"memd/internal/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

// baseEnv is a valid environment.
func baseEnv() map[string]string {
	return map[string]string{
		envDSN: "postgres://x", envGatewayURL: "http://gw/v1", envS3Bucket: "memd", envS3PathStyle: "true",
		envStaleAfter: "8s", envHeartbeatInterval: "1s",
	}
}

// TestLoadConfigEnvAndFlags checks defaults, environment values, flags winning over the environment, and
// the e2e harness's variable names (decision 1411).
func TestLoadConfigEnvAndFlags(t *testing.T) {
	env := baseEnv()
	cfg, err := LoadConfig([]string{"all", "-grpc-addr", "127.0.0.1:9", "-worker-slots=4"},
		func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{
		"mode":         cfg.Mode == modeAll,
		"dsn":          cfg.DSN == "postgres://x",
		"flag wins":    cfg.GRPCAddr == "127.0.0.1:9",
		"slots flag":   cfg.WorkerSlots == 4,
		"path style":   cfg.S3PathStyle,
		"stale env":    cfg.StaleAfter == 8*time.Second,
		"default poll": cfg.PollInterval == defaultPollInterval,
		"embed model":  cfg.EmbedModel == defaultEmbedModel,
		"env kept":     cfg.Env(envDSN) == "postgres://x",
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
		"heartbeat too slow": func(env map[string]string) []string {
			env[envHeartbeatInterval] = "6s"
			return []string{"worker"}
		},
		"zero slots": func(env map[string]string) []string { env[envWorkerSlots] = "0"; return []string{"all"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := baseEnv()
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

// TestUsage checks a missing mode is a usage error and the usage text never says "not implemented" (the
// e2e harness skips on that marker, decision 1411).
func TestUsage(t *testing.T) {
	if _, err := LoadConfig(nil, func(string) string { return "" }); !errors.Is(err, errUsage) {
		t.Fatalf("no mode: %v", err)
	}
	var buf bytes.Buffer
	usage(&buf)
	if !strings.Contains(buf.String(), envDSN) || strings.Contains(buf.String(), "not implemented") {
		t.Fatalf("usage text:\n%s", buf.String())
	}
}

// embedder is a port.Embedder returning vectors of a fixed size.
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

// TestTokenCounterConcurrent shares one counter across goroutines, as every RPC and worker slot does; run
// with -race.
func TestTokenCounterConcurrent(t *testing.T) {
	tok, err := newTokenCounter()
	if err != nil {
		t.Fatal(err)
	}
	want := tok.Count("Alice moved to Lisbon in 2021.")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			for j := range 50 {
				if got := tok.Count("Alice moved to Lisbon in 2021."); got != want {
					t.Errorf("goroutine %d: %d tokens, want %d", i, got, want)
				}
				tok.Count(strings.Repeat("token ", i+j))
			}
		})
	}
	wg.Wait()
}

// TestTokenCounter checks the embedded tiktoken encoding counts tokens without a download.
func TestTokenCounter(t *testing.T) {
	tok, err := newTokenCounter()
	if err != nil {
		t.Fatal(err)
	}
	if n := tok.Count("hello world"); n != 2 {
		t.Fatalf("hello world = %d tokens, want 2", n)
	}
	if n := tok.Count(""); n != 0 {
		t.Fatalf("empty = %d", n)
	}
}
