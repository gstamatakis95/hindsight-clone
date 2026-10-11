package main

import (
	"context"
	"io"
	"log/slog"
	"memd/internal/ai"
	"memd/internal/ai/gateway"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/grpcapi"
	"memd/internal/observability"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"memd/test/e2e/gatewayemu"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	pb "memd/gen/memory/v1"
)

// The shared test stack: the whole composition (wireEngines, the gRPC server and the worker) over a fresh pgtest
// database, an in-memory blob store, the real gateway client and the gateway emulator, served over bufconn. Tests of
// the API's behaviour through the real engines (api_test.go) and of the wiring itself (wire_test.go) run on it.

// txGuard records any gateway call made from inside a transaction body. The worker, the maintenance tick and
// the API run concurrently in one process, so "some transaction is open somewhere" is not the test: a call
// counts only when the calling goroutine's own stack is inside guardStore.InTx.
type txGuard struct {
	mu         sync.Mutex
	violations []string
}

// inTxFrame is the stack frame prefix of a transaction body run through guardStore.InTx.
const inTxFrame = "memd/cmd/memd.guardStore.InTx"

// insideTx reports whether the calling goroutine is running inside guardStore.InTx.
func insideTx() bool {
	buf := make([]byte, 64<<10)
	return strings.Contains(string(buf[:runtime.Stack(buf, false)]), inTxFrame)
}

// guardStore is a store.DB that tells the guard when a transaction is open.
type guardStore struct {
	store.DB
	g *txGuard
}

// InTx marks the transaction open while fn runs.
func (s guardStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error { //nolint:wrapcheck // test passthrough
		return fn(tx)
	})
}

// guardTransport records gateway requests sent while a transaction is open.
type guardTransport struct{ g *txGuard }

// RoundTrip forwards r after checking the caller is not inside a transaction body.
func (t guardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if insideTx() {
		t.g.mu.Lock()
		t.g.violations = append(t.g.violations, r.URL.Path)
		t.g.mu.Unlock()
	}
	return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // test passthrough
}

// wiredStack is the whole composition (wireEngines) over pgtest, a fake blob store, the real gateway client and
// the emulated gateway, served over bufconn, with one worker slot so jobs run one at a time.
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
	db := pgtest.NewStore(t)
	gw := gateway.NewClient(gateway.Config{
		BaseURL: ts.URL + gatewayemu.BasePath, EmbedModel: config.DefaultEmbedModel, TraceCall: tel.GatewayHook(),
	}, tel.HTTPClient(&http.Client{Transport: guardTransport{g: g}}))
	models := &domain.ModelBindings{}
	for _, slot := range append(append([]domain.ModelSlot{}, domain.LLMSlots...), domain.ModelSlotRerank) {
		*models.Slot(slot) = &domain.ModelBinding{Model: gatewayemu.ModelFor(string(slot))}
	}
	cfg := Config{WorkerSlots: 1, GatewayURL: ts.URL, File: ServerFile{
		Defaults: domain.BankConfig{Models: models},
		Worker:   WorkerFile{PollInterval: Duration(10 * time.Millisecond), HeartbeatInterval: Duration(time.Second)},
	}}
	cfg.File.withDefaults()
	serverCfg, err := cfg.File.validate()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ai.NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := infra{db: guardStore{DB: db, g: g}, graph: db.Graph(), claimer: db, maint: db, blob: fake.NewBlob(), gw: gw}
	d := wireEngines(cfg, in, tok, serverCfg, clock.System{}, log)
	d.probes = nil
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

// ctx is a context for tenant "t" with a deadline.
func (w *wiredStack) ctx(t *testing.T) context.Context {
	t.Helper()
	return w.ctxAs(t, "t")
}

// ctxAs is a context for tenant with a deadline.
func (w *wiredStack) ctxAs(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, grpcapi.TenantHeader, tenant)
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
