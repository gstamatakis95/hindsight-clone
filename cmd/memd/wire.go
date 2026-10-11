package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/ai"
	"memd/internal/ai/gateway"
	"memd/internal/blob"
	"memd/internal/blob/s3"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/recall"
	"memd/internal/engine/reflect"
	"memd/internal/engine/retain"
	"memd/internal/engine/timex"
	"memd/internal/observability"
	"memd/internal/server"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/worker"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// The composition root. wire.go opens the infrastructure (Postgres, blob storage, the AI gateway), builds the shared
// libraries once (core) and runs the two modes; each wire_<area>.go builds one area's engines and fills its part of the
// gRPC server's dependencies, the job handlers and the maintenance tick.

// modelGateway is the AI gateway as the engines see it: one client serves all three model interfaces.
type modelGateway interface {
	ai.LLM
	ai.Embedder
	ai.Reranker
}

// infra are the adapters, each narrowed to its boundary. openInfra is the only place the concrete adapter types
// appear; tests build infra over pgtest and internal/fake.
type infra struct {
	db      store.DB
	claimer store.Claimer
	maint   store.Maintenance
	blob    blob.Store
	gw      modelGateway
	ping    pinger
}

// deps is everything the two modes run.
type deps struct {
	cfg         Config
	log         *slog.Logger
	server      server.Deps
	handlers    worker.Handlers
	maintenance worker.MaintenanceDeps
	claimer     store.Claimer
	pending     worker.PendingChecker
	clock       clock.Clock
	// tel is the optional Datadog instrumentation; nil (the default) installs nothing.
	tel *observability.Telemetry
}

// buildDeps connects the adapters, checks the gateway's embedding dimension and builds every engine. The
// returned closer releases the Postgres pool. tel (nil = Datadog off) instruments the adapters, the gRPC
// server and the worker.
func buildDeps(ctx context.Context, cfg Config, log *slog.Logger, tel *observability.Telemetry,
) (*deps, func(), error) {
	in, closer, err := openInfra(ctx, cfg, tel)
	if err != nil {
		return nil, nil, err
	}
	if err := checkEmbedding(ctx, in.gw); err != nil {
		closer()
		return nil, nil, err
	}
	tok, err := ai.NewTokenizer()
	if err != nil {
		closer()
		return nil, nil, fmt.Errorf("memd: %w", err)
	}
	serverCfg, err := config.ServerDefaults(cfg.Env)
	if err != nil {
		closer()
		return nil, nil, fmt.Errorf("memd: %w", err)
	}
	d := wireEngines(cfg, in, tok, serverCfg, clock.System{}, log)
	d.tel = tel
	return d, closer, nil
}

// openInfra opens the Postgres pool, the blob store and the gateway client. tel's instrumentation (no-ops when nil)
// goes into each: the pgx query tracer, the S3 middleware, and the gateway's call hook and HTTP client.
func openInfra(ctx context.Context, cfg Config, tel *observability.Telemetry) (infra, func(), error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return infra{}, nil, fmt.Errorf("memd: %s: %w", envDSN, err)
	}
	pcfg.MaxConns = int32(poolSize(cfg)) //nolint:gosec // poolSize is small and positive
	tel.TracePool(pcfg)
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return infra{}, nil, fmt.Errorf("memd: postgres: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return infra{}, nil, fmt.Errorf("memd: postgres: %w", err)
	}
	bs, err := openBlob(ctx, cfg, tel)
	if err != nil {
		pool.Close()
		return infra{}, nil, err
	}
	gw := gateway.NewClient(gateway.Config{
		BaseURL: cfg.GatewayURL, APIKey: cfg.GatewayAPIKey, EmbedModel: cfg.EmbedModel,
		MaxConcurrent: cfg.GatewayMaxConcurrent, SlotConcurrency: cfg.SlotConcurrency, Timeout: cfg.GatewayTimeout,
		TraceCall: tel.GatewayHook(),
	}, tel.HTTPClient(nil))
	pg := postgres.New(pool)
	return infra{db: pg, claimer: pg, maint: pg, blob: bs, gw: gw, ping: pool}, pool.Close, nil
}

// openBlob builds the blob.Store adapter MEMD_BLOB_PROVIDER selects (Config.Validate has already rejected an
// unknown provider).
func openBlob(ctx context.Context, cfg Config, tel *observability.Telemetry) (blob.Store, error) {
	switch cfg.BlobProvider {
	case blobProviderS3:
		st, err := s3.New(ctx, s3.Config{
			Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, AccessKeyID: cfg.S3AccessKey,
			SecretAccessKey: cfg.S3SecretKey, UsePathStyle: cfg.S3PathStyle, APIOptions: tel.S3APIOptions(),
		})
		if err != nil {
			return nil, fmt.Errorf("memd: s3: %w", err)
		}
		return st, nil
	default:
		return nil, fmt.Errorf("memd: %s: unknown blob provider %q", envBlobProvider, cfg.BlobProvider)
	}
}

// poolSize is the Postgres pool size: the configured one, else enough for every worker slot (each may hold a
// transaction while Read() runs beside it), the heartbeats and maintenance, and the api's concurrent RPCs.
func poolSize(cfg Config) int {
	if cfg.DBMaxConns > 0 {
		return cfg.DBMaxConns
	}
	return max(2*cfg.WorkerSlots+16, 32)
}

// checkEmbedding fails startup unless the gateway's embedding model returns domain.EmbeddingDim (768)
// dimensions: memory_units.embedding is vector(768), so any other model would fail every write later.
func checkEmbedding(ctx context.Context, emb ai.Embedder) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	vec, err := emb.EmbedQuery(ctx, "memd startup embedding check")
	if err != nil {
		return fmt.Errorf("memd: gateway embedding check: %w", err)
	}
	if len(vec) != domain.EmbeddingDim {
		return fmt.Errorf("memd: gateway embedding model returns %d dimensions, want %d (%s)",
			len(vec), domain.EmbeddingDim, envEmbedModel)
	}
	return nil
}

// core are the libraries and engines several areas share.
type core struct {
	db        store.DB
	read      store.Reader
	clock     clock.Clock
	tok       *ai.Tokenizer
	resolver  config.Resolver
	router    *ai.Router
	matcher   consolidate.StrategyMatcher
	times     timex.TimeNormalizer
	scopes    consolidate.ScopeExpander
	linker    retain.Linker
	entities  retain.EntityResolver
	retainer  retain.Retainer
	recaller  recall.Recaller
	reflector reflect.Reflector
	queuer    mentalmodel.RefreshQueuer
	dirs      bank.DirectiveSelector
}

// wireEngines builds the shared libraries, then every area over them.
func wireEngines(
	cfg Config, in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock, log *slog.Logger,
) *deps {
	c := wireCore(in, tok, serverCfg, clk)
	d := &deps{
		cfg: cfg, log: log, claimer: in.claimer, clock: clk, pending: worker.NewPendingChecker(in.db, clk),
		handlers: worker.Handlers{
			Store: in.db, Clock: clk,
			Checkpoint: func(j store.Job) worker.Checkpoint { return worker.NewCheckpoint(in.claimer, j.ID) },
		},
		server: server.Deps{
			DefaultDeadline: cfg.RPCDeadline,
			Probes: []server.ReadinessProbe{
				postgresProbe{db: in.ping}, &gatewayProbe{url: cfg.GatewayURL, hc: &http.Client{}},
			},
		},
	}
	wireMemory(c, in, d)
	wireDocuments(c, in, d)
	wireObservations(c, in, d)
	wireMentalModels(c, in, d)
	wireBanks(c, in, d)
	wireConfig(c, d)
	wireOperations(c, d)
	wireMaintenance(cfg, c, in, d)
	return d
}

// wireCore builds config resolution, routing, time normalization, retain's shared pieces, recall and reflect.
func wireCore(in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock) core {
	db, claimer, gw := in.db, in.claimer, in.gw
	c := core{db: db, read: db.Read(), clock: clk, tok: tok, router: ai.NewRouter()}
	c.matcher = consolidate.NewStrategyMatcher(c.read)
	c.resolver = config.NewResolver(db, c.matcher, serverCfg)
	c.times = timex.NewNormalizer(gw, c.router, clk)
	c.scopes = consolidate.NewScopeExpander()
	c.linker = retain.NewLinker()
	c.entities = retain.NewEntityResolver(retain.ResolverConfig{})
	c.retainer = retain.NewRetainer(retain.RetainerDeps{
		Store: db, Blob: in.blob, Config: c.resolver, Extractor: retain.NewExtractor(gw, c.router),
		Dates: c.times, Embedder: gw, Entities: c.entities, Labels: retain.NewLabelResolver(), Linker: c.linker,
		Scopes: c.scopes, Tokens: tok, Clock: clk,
		Checkpoint: func(id domain.OperationID) retain.Checkpoint { return worker.NewCheckpoint(claimer, id) },
	})
	c.recaller = recall.NewRecaller(recall.RecallerDeps{
		Reader: c.read, Config: c.resolver, Embedder: gw, Reranker: gw, Router: c.router, Times: c.times,
		Clock: clk, Count: tok,
	})
	c.queuer = mentalmodel.NewRefreshQueuer(clk)
	c.dirs = bank.NewDirectiveSelector(db)
	pages := &pageReader{search: mentalmodel.NewPageSearcher(c.read, gw), s: db}
	c.reflector = reflect.NewReflector(reflect.ReflectorDeps{
		Store: db, Config: c.resolver, Router: c.router, LLM: gw, Clock: clk,
		Tools: reflect.DefaultTools(c.recaller, pages, c.read),
	})
	return c
}

// apiShutdownGrace is how long in-flight RPCs may finish after a stop signal.
const apiShutdownGrace = 20 * time.Second

// runAPI serves gRPC on cfg.GRPCAddr until ctx is done, then stops gracefully.
func runAPI(ctx context.Context, d *deps) error {
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("memd: listen %s: %w", d.cfg.GRPCAddr, err)
	}
	srv := newGRPCServer(d)
	reflection.Register(srv) // lets grpcurl and similar tools discover the services
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	d.log.Info("memd api serving", "addr", lis.Addr().String())
	select {
	case err := <-served:
		return fmt.Errorf("memd: serve: %w", err)
	case <-ctx.Done():
	}
	stopped := make(chan struct{})
	go func() { srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(apiShutdownGrace):
		srv.Stop()
	}
	return nil
}

// newGRPCServer builds the gRPC server over d, traced when d.tel is enabled.
func newGRPCServer(d *deps) *grpc.Server {
	return server.New(d.server, d.tel.GRPCServerOptions(server.TenantFrom)...)
}

// runWorker runs the job runner and the maintenance tick until ctx is done. The runner's StaleAfter and
// the maintenance reclaim's StaleAfter come from the one config value, so the runner's self-fence (3/4 of
// it) always fires before another worker can reclaim the job. The worker id is "<MEMD_WORKER_ID or
// hostname>-<uuid>", fresh per process (effectiveWorkerID). With MEMD_WORKER_HEALTH_ADDR set it also serves
// grpc.health.v1 there.
func runWorker(ctx context.Context, d *deps) error {
	id := effectiveWorkerID(d.cfg.WorkerID, os.Hostname, uuid.NewString)
	r := worker.NewRunner(d.claimer, worker.RunnerConfig{
		WorkerID: id, Clock: d.clock, Slots: d.cfg.WorkerSlots, PollInterval: d.cfg.PollInterval,
		HeartbeatInterval: d.cfg.HeartbeatInterval, StaleAfter: d.cfg.StaleAfter, Pending: d.pending, Logger: d.log,
		TraceJob: d.tel.JobHook(),
	})
	worker.Register(r, d.handlers)
	maint := d.maintenance
	maint.TraceTick = d.tel.TickHook()
	var lis net.Listener
	if d.cfg.WorkerHealthAddr != "" {
		var err error
		if lis, err = (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.WorkerHealthAddr); err != nil {
			return fmt.Errorf("memd: listen %s: %w", d.cfg.WorkerHealthAddr, err)
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return r.Run(gctx) })
	g.Go(func() error { return worker.RunMaintenance(gctx, maint) })
	if lis != nil {
		g.Go(func() error { return serveHealth(gctx, lis, d.server.Probes) })
		d.log.Info("memd worker health serving", "addr", lis.Addr().String())
	}
	d.log.Info("memd worker running", "id", id, "slots", d.cfg.WorkerSlots)
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("memd: worker: %w", err)
	}
	return nil
}
