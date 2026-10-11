package main

import (
	"context"
	"io"
	"log/slog"
	"memd/internal/adapter/gateway"
	"memd/internal/config"
	"memd/internal/fake"
	"memd/internal/observability"
	"memd/internal/port"
	"memd/internal/server"
	"memd/test/e2e/gatewayemu"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	pb "memd/gen/memory/v1"
)

// txGuard counts open transactions and records any gateway call made while one is open.
type txGuard struct {
	open       atomic.Int64
	mu         sync.Mutex
	violations []string
}

// guardStore is a port.Store that tells the guard when a transaction is open.
type guardStore struct {
	port.Store
	g *txGuard
}

// InTx marks the transaction open while fn runs.
func (s guardStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error { //nolint:wrapcheck // test passthrough
		s.g.open.Add(1)
		defer s.g.open.Add(-1)
		return fn(tx)
	})
}

// guardTransport records gateway requests sent while a transaction is open.
type guardTransport struct{ g *txGuard }

// RoundTrip forwards r after checking no transaction is open.
func (t guardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.g.open.Load() > 0 {
		t.g.mu.Lock()
		t.g.violations = append(t.g.violations, r.URL.Path)
		t.g.mu.Unlock()
	}
	return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // test passthrough
}

// wiredStack is the whole composition (wireEngines) over internal/fake, the real gateway client and the
// emulated gateway, served over bufconn, with one worker slot so jobs run one at a time.
type wiredStack struct {
	guard *txGuard
	conn  *grpc.ClientConn
	deps  *deps
}

// startWired builds the stack and runs the worker until the test ends.
func startWired(t *testing.T) *wiredStack {
	t.Helper()
	return startWiredWith(t, nil)
}

// startWiredWith is startWired with tel's instrumentation (nil = Datadog off) on the gateway client, the
// gRPC server and the worker, wired as buildDeps and runAPI wire them.
func startWiredWith(t *testing.T, tel *observability.Telemetry) *wiredStack {
	t.Helper()
	_, ts := gatewayemu.Start(gatewayemu.Options{})
	t.Cleanup(ts.Close)
	g := &txGuard{}
	store := fake.NewStore(systemClock{})
	gw := gateway.NewClient(gateway.Config{
		BaseURL: ts.URL + gatewayemu.BasePath, EmbedModel: defaultEmbedModel, TraceCall: tel.GatewayHook(),
	}, tel.HTTPClient(&http.Client{Transport: guardTransport{g: g}}))
	env := map[string]string{}
	for _, slot := range []string{
		"extract", "time_parse", "consolidate", "dedup_check", "reflect", "structured_output",
		"mental_model_delta", "synthesize", "rerank",
	} {
		env[config.EnvModelPrefix+strings.ToUpper(slot)] = gatewayemu.ModelFor(slot)
	}
	serverCfg, err := config.ServerDefaults(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	tok, err := newTokenCounter()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		WorkerSlots: 1, PollInterval: 10 * time.Millisecond, HeartbeatInterval: time.Second,
		StaleAfter: time.Minute, MaintenanceInterval: time.Minute, GatewayURL: ts.URL,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := ports{store: guardStore{Store: store, g: g}, claimer: store, blob: fake.NewBlob(), gw: gw}
	d := wireEngines(cfg, p, tok, serverCfg, systemClock{}, log)
	d.server.Probes = nil
	d.tel = tel
	lis := bufconn.Listen(1 << 20)
	srv := newGRPCServer(d)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runWorker(ctx, d); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &wiredStack{guard: g, conn: conn, deps: d}
}

// ctx is a tenant context with a deadline.
func (w *wiredStack) ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, server.TenantHeader, "t")
}

// idle waits until the bank has no pending or processing operation.
func (w *wiredStack) idle(t *testing.T, bank string) {
	t.Helper()
	banks := pb.NewBankServiceClient(w.conn)
	ctx := w.ctx(t)
	for {
		resp, err := banks.GetBankStats(ctx, &pb.GetBankStatsRequest{BankId: bank})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.GetStats().GetPendingOperationsByKind()) == 0 {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("not idle: %v", resp.GetStats().GetPendingOperationsByKind())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWiredStackNoModelCallInsideTransaction runs the composition root's whole graph (every engine as
// wire.go builds it, over internal/fake and the emulated gateway) through retain, consolidation, recall,
// reflect, both refresh strategies and curation, and checks that no gateway call was ever made while a
// transaction was open (SPEC "Jobs": no model calls inside a transaction).
func TestWiredStackNoModelCallInsideTransaction(t *testing.T) {
	w := startWired(t)
	const bank = "b"
	mem := pb.NewMemoryServiceClient(w.conn)
	units := pb.NewMemoryUnitServiceClient(w.conn)
	models := pb.NewMentalModelServiceClient(w.conn)
	ret, err := mem.Retain(w.ctx(t), &pb.RetainRequest{BankId: bank, Items: []*pb.RetainItem{{
		Content: "Alice moved to Lisbon in 2021. Alice works as a nurse.", DocumentId: "d1",
	}}})
	if err != nil || ret.GetDocuments()[0].GetUnitsCreated() == 0 {
		t.Fatalf("retain: %v %v", ret, err)
	}
	w.idle(t, bank)
	obs, err := units.ListMemories(w.ctx(t), &pb.ListMemoriesRequest{
		BankId: bank, Types: []pb.FactType{pb.FactType_FACT_TYPE_OBSERVATION},
	})
	if err != nil || len(obs.GetItems()) == 0 {
		t.Fatalf("observations: %v %v", obs, err)
	}
	rec, err := mem.Recall(w.ctx(t), &pb.RecallRequest{BankId: bank, Query: "Where does Alice live? Lisbon"})
	if err != nil || len(rec.GetResults()) == 0 {
		t.Fatalf("recall: %v %v", rec, err)
	}
	ref, err := mem.Reflect(w.ctx(t), &pb.ReflectRequest{BankId: bank, Query: "Where does Alice live?"})
	if err != nil || ref.GetText() == "" {
		t.Fatalf("reflect: %v %v", ref, err)
	}
	w.mentalModelBothStrategies(t, models, bank)
	facts, err := units.ListMemories(w.ctx(t), &pb.ListMemoriesRequest{
		BankId: bank, Types: []pb.FactType{pb.FactType_FACT_TYPE_WORLD, pb.FactType_FACT_TYPE_EXPERIENCE},
	})
	if err != nil || len(facts.GetItems()) == 0 {
		t.Fatalf("facts: %v %v", facts, err)
	}
	fact := facts.GetItems()[0].GetId()
	for _, st := range []pb.MemoryState{pb.MemoryState_MEMORY_STATE_INVALIDATED, pb.MemoryState_MEMORY_STATE_VALID} {
		if _, err := units.UpdateMemory(w.ctx(t), &pb.UpdateMemoryRequest{
			BankId: bank, MemoryId: fact, State: st, Reason: "test",
		}); err != nil {
			t.Fatalf("update memory %s: %v", st, err)
		}
		w.idle(t, bank)
	}
	w.guard.mu.Lock()
	defer w.guard.mu.Unlock()
	if len(w.guard.violations) > 0 {
		t.Fatalf("gateway calls inside a transaction: %v", w.guard.violations)
	}
}

// mentalModelBothStrategies builds a model agentically, then refreshes it single-shot.
func (w *wiredStack) mentalModelBothStrategies(t *testing.T, models pb.MentalModelServiceClient, bank string) {
	t.Helper()
	if _, err := models.CreateMentalModel(w.ctx(t), &pb.CreateMentalModelRequest{BankId: bank, Spec: &pb.MentalModelSpec{
		Id: "alice", Name: "Alice", SourceQuery: "Who is Alice?",
		Trigger: &pb.RefreshTrigger{Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC},
	}}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, bank)
	if _, err := models.RefreshMentalModel(w.ctx(t), &pb.RefreshMentalModelRequest{
		BankId: bank, MentalModelId: "alice", ForceFull: true, Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
	}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, bank)
	m, err := models.GetMentalModel(w.ctx(t), &pb.GetMentalModelRequest{BankId: bank, MentalModelId: "alice"})
	if err != nil || m.GetMentalModel().GetVersion() < 2 || m.GetMentalModel().GetContentMd() == "" {
		t.Fatalf("mental model after two refreshes: %v %v", m, err)
	}
}
