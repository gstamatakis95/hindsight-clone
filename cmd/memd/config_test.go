package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/routing"
	"memd/internal/server"
	"memd/internal/worker"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// baseEnv is a valid environment.
func baseEnv() map[string]string {
	return map[string]string{
		envDSN: "postgres://x", envGatewayURL: "http://gw/v1", envS3Bucket: "memd", envS3PathStyle: "true",
		envS3AccessKey: "ak", envS3SecretKey: "sk", envStaleAfter: "8s", envHeartbeatInterval: "1s",
		config.EnvModel: "m",
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

// TestEffectiveWorkerID (WP-20 item 4): "<MEMD_WORKER_ID or hostname>-<uuid>", unique per process.
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

// TestValidateS3Keys (WP-20 item 4): both static S3 keys are required.
func TestValidateS3Keys(t *testing.T) {
	for _, drop := range [][]string{{envS3AccessKey}, {envS3SecretKey}, {envS3AccessKey, envS3SecretKey}} {
		env := baseEnv()
		for _, k := range drop {
			delete(env, k)
		}
		if _, err := loadValidate([]string{"api"}, env); err == nil || !strings.Contains(err.Error(), envS3AccessKey) {
			t.Errorf("without %v: %v, want the S3 key error", drop, err)
		}
	}
	if _, err := loadValidate([]string{"api"}, baseEnv()); err != nil {
		t.Fatalf("both keys: %v", err)
	}
}

// TestRequiredSlots (WP-20 item 4): startup fails when EXTRACT or REFLECT has no model.
func TestRequiredSlots(t *testing.T) {
	env := baseEnv()
	delete(env, config.EnvModel)
	_, err := loadValidate([]string{"all"}, env)
	for _, slot := range []string{"EXTRACT", "REFLECT"} {
		if err == nil || !strings.Contains(err.Error(), config.EnvModelPrefix+slot) {
			t.Errorf("no models: %v, want a %s error", err, slot)
		}
	}
	env[config.EnvModelPrefix+"EXTRACT"] = "x"
	if _, err := loadValidate([]string{"all"}, env); err == nil || !strings.Contains(err.Error(), "REFLECT") {
		t.Errorf("only EXTRACT bound: %v, want a REFLECT error", err)
	}
	env[config.EnvModelPrefix+"REFLECT"] = "r"
	if _, err := loadValidate([]string{"all"}, env); err != nil {
		t.Errorf("EXTRACT and REFLECT bound: %v", err)
	}
}

// TestUnboundSlotRouter (WP-20 item 4): a call on an unbound slot is FAILED_PRECONDITION (and a permanent job
// failure), not INVALID_ARGUMENT; bound slots and EMBED keep the engine router's behaviour.
func TestUnboundSlotRouter(t *testing.T) {
	r := slotRouter{routing.NewRouter()}
	cfg := domain.BankConfig{Models: []domain.SlotBinding{
		{Slot: domain.ModelSlotExtract, Binding: domain.ModelBinding{Model: "x"}},
	}}
	if b, err := r.Resolve(cfg, domain.ModelSlotExtract); err != nil || b.Model != "x" {
		t.Fatalf("bound slot: %+v %v", b, err)
	}
	_, err := r.Resolve(cfg, domain.ModelSlotConsolidate)
	if !errors.Is(err, domain.ErrFailedPrecondition) || errors.Is(err, domain.ErrInvalidArgument) ||
		!errors.Is(err, worker.ErrPermanent) {
		t.Fatalf("unbound slot: %v, want FailedPrecondition + ErrPermanent, not InvalidArgument", err)
	}
	if _, err := r.Resolve(cfg, domain.ModelSlotEmbed); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("embed: %v, want the engine router's InvalidArgument", err)
	}
}

// TestSlotConcurrencyEnv (WP-20 item 4): MEMD_GATEWAY_MAX_CONCURRENT_<SLOT> reaches Config.SlotConcurrency
// (and from there gateway.Config.SlotConcurrency); bad values are rejected.
func TestSlotConcurrencyEnv(t *testing.T) {
	env := baseEnv()
	env[envGatewayConcurrency+"_EXTRACT"] = "3"
	env[envGatewayConcurrency+"_EMBED"] = " 7 "
	cfg, err := loadValidate([]string{"worker"}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.ModelSlot]int{domain.ModelSlotExtract: 3, domain.ModelSlotEmbed: 7}
	if !maps.Equal(cfg.SlotConcurrency, want) {
		t.Fatalf("SlotConcurrency = %v, want %v", cfg.SlotConcurrency, want)
	}
	for _, bad := range []string{"x", "0", "-1"} {
		env[envGatewayConcurrency+"_RERANK"] = bad
		if _, err := loadValidate([]string{"worker"}, env); err == nil {
			t.Errorf("%s_RERANK=%q accepted", envGatewayConcurrency, bad)
		}
	}
}

// failingProbe is a readiness probe that fails while fail is set.
type failingProbe struct{ fail atomic.Bool }

func (p *failingProbe) Ready(context.Context) error {
	if p.fail.Load() {
		return errors.New("postgres down")
	}
	return nil
}

// TestWorkerHealthServer (WP-20 item 4): the worker's health endpoint answers Check from the probes and
// stops when its context ends.
func TestWorkerHealthServer(t *testing.T) {
	probe := &failingProbe{}
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveHealth(ctx, lis, []server.ReadinessProbe{probe}) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	hc := healthpb.NewHealthClient(conn)
	check := func() healthpb.HealthCheckResponse_ServingStatus {
		t.Helper()
		resp, err := hc.Check(t.Context(), &healthpb.HealthCheckRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetStatus()
	}
	if s := check(); s != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("healthy: %v", s)
	}
	probe.fail.Store(true)
	if s := check(); s != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("probe failing: %v", s)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHealth: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveHealth did not stop")
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
