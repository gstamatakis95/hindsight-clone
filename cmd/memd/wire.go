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
	"memd/internal/graph"
	"memd/internal/grpcapi"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/maintenance"
	"memd/internal/observability"
	"memd/internal/recall"
	"memd/internal/reflect"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/timex"
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
// libraries once (core) and runs the two modes; each wire_<area>.go builds one area's engines, its gRPC handlers, its
// job handlers and its part of the maintenance tick.

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
	graph   graph.Reader
	claimer jobs.Claimer
	maint   store.Maintenance
	blob    blob.Store
	gw      modelGateway
	ping    func(ctx context.Context) error
}

// deps is everything the two modes run.
type deps struct {
	cfg      Config
	log      *slog.Logger
	api      grpcapi.Handlers
	probes   []grpcapi.ReadinessProbe
	handlers handlers
	// jobHandlers are the job handlers each wire_<area>.go registers by kind; runWorker adds them to the runner.
	jobHandlers map[domain.OperationKind]jobs.Handler
	maintenance maintenance.Deps
	claimer     jobs.Claimer
	db          store.DB
	clock       clock.Clock
	// tel is the optional Datadog instrumentation; nil (the default) installs nothing.
	tel *observability.Telemetry
}

// buildDeps connects the adapters, checks the gateway's embedding dimension and builds every engine. The
// returned closer releases the Postgres pool. tel (nil = Datadog off) instruments the adapters, the gRPC
// server and the worker.
func buildDeps(ctx context.Context, cfg Config, log *slog.Logger, tel *observability.Telemetry,
) (*deps, func(), error) {
	serverCfg, err := cfg.File.validate()
	if err != nil {
		return nil, nil, fmt.Errorf("memd: config file: %w", err)
	}
	in, closer, err := openInfra(ctx, cfg, serverCfg.Models.Embed.Model, tel)
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
	d := wireEngines(cfg, in, tok, serverCfg, clock.System{}, log)
	d.tel = tel
	return d, closer, nil
}

// openInfra opens the Postgres pool, the blob store and the gateway client, which embeds with embedModel. tel's
// instrumentation (no-ops when nil) goes into each: the pgx query tracer, the S3 middleware, and the gateway's call
// hook and HTTP client.
func openInfra(ctx context.Context, cfg Config, embedModel string, tel *observability.Telemetry,
) (infra, func(), error) {
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
		BaseURL: cfg.GatewayURL, APIKey: cfg.GatewayAPIKey, EmbedModel: embedModel,
		MaxConcurrent: cfg.File.Gateway.MaxConcurrent, SlotConcurrency: cfg.File.Gateway.SlotConcurrency,
		Timeout:   time.Duration(cfg.File.Gateway.Timeout),
		TraceCall: tel.GatewayHook(),
	}, tel.HTTPClient(nil))
	pg := postgres.New(pool)
	return infra{db: pg, graph: pg.Graph(), claimer: pg, maint: pg, blob: bs, gw: gw, ping: pool.Ping}, pool.Close, nil
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
		return fmt.Errorf("memd: gateway embedding model returns %d dimensions, want %d (defaults.models.embed "+
			"in the server config file)", len(vec), domain.EmbeddingDim)
	}
	return nil
}

// core are the libraries and engines several areas share.
type core struct {
	db       store.DB
	read     store.Reader
	clock    clock.Clock
	tok      *ai.Tokenizer
	resolver *config.Resolver
	router   *ai.Router
	times    *timex.Normalizer
	linker   *linking.Linker
	entities *linking.Resolver
	recall   *recall.Engine
	agent    *reflect.Agent
}

// wireEngines builds the shared libraries, then every area over them.
func wireEngines(
	cfg Config, in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock, log *slog.Logger,
) *deps {
	c := wireCore(in, tok, serverCfg, clk)
	d := &deps{
		cfg: cfg, log: log, claimer: in.claimer, db: in.db, clock: clk,
		handlers: handlers{db: in.db, clock: clk}, jobHandlers: map[domain.OperationKind]jobs.Handler{},
		probes: []grpcapi.ReadinessProbe{
			grpcapi.PostgresProbe{Ping: in.ping}, grpcapi.NewGatewayProbe(cfg.GatewayURL, &http.Client{}),
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

// wireCore builds the libraries: config resolution, routing, time normalization, linking, recall (over the store
// and the graph reader) and the reflect agent.
func wireCore(in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock) core {
	db, gw := in.db, in.gw
	c := core{db: db, read: db.Read(), clock: clk, tok: tok, router: ai.NewRouter()}
	c.resolver = config.NewResolver(db, serverCfg)
	c.times = timex.New(gw, c.router, clk)
	c.linker = linking.NewLinker()
	c.entities = linking.NewResolver(linking.ResolverConfig{})
	c.recall = recall.New(recall.Deps{
		DB: db, Graph: in.graph, Config: c.resolver, Embedder: gw, Reranker: gw, Router: c.router, Times: c.times,
		Clock: clk, Tokens: tok,
	})
	c.agent = reflect.New(reflect.Deps{
		DB: db, Config: c.resolver, Router: c.router, LLM: gw, Clock: clk, Recall: c.recall,
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
	o := grpcapi.Options{DefaultDeadline: time.Duration(*d.cfg.File.API.RPCDeadline), Probes: d.probes}
	return grpcapi.New(d.api, o, d.tel.GRPCServerOptions(grpcapi.TenantFrom)...)
}

// runWorker runs the job runner and the maintenance tick until ctx is done. The runner's StaleAfter and
// the maintenance reclaim's StaleAfter come from the one config value, so the runner's self-fence (3/4 of
// it) always fires before another worker can reclaim the job. The worker id is "<MEMD_WORKER_ID or
// hostname>-<uuid>", fresh per process (effectiveWorkerID). With MEMD_WORKER_HEALTH_ADDR set it also serves
// grpc.health.v1 there.
func runWorker(ctx context.Context, d *deps) error {
	id := effectiveWorkerID(d.cfg.WorkerID, os.Hostname, uuid.NewString)
	w := d.cfg.File.Worker
	r := jobs.New(d.claimer, jobs.Config{
		WorkerID: id, Clock: d.clock, Slots: d.cfg.WorkerSlots, PollInterval: time.Duration(w.PollInterval),
		HeartbeatInterval: time.Duration(w.HeartbeatInterval), StaleAfter: time.Duration(w.StaleAfter), DB: d.db,
		Logger:   d.log,
		TraceJob: d.tel.JobHook(),
	})
	md := d.maintenance
	md.TraceTick = d.tel.TickHook()
	maint := maintenance.New(md)
	h := d.handlers
	h.graph = maint
	h.register(r)
	for kind, fn := range d.jobHandlers {
		r.Handle(kind, fn)
	}

	var lis net.Listener
	if d.cfg.WorkerHealthAddr != "" {
		var err error
		if lis, err = (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.WorkerHealthAddr); err != nil {
			return fmt.Errorf("memd: listen %s: %w", d.cfg.WorkerHealthAddr, err)
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return r.Run(gctx) })
	g.Go(func() error { return maint.Run(gctx) })
	if lis != nil {
		g.Go(func() error { return grpcapi.ServeHealth(gctx, lis, d.probes, apiShutdownGrace) })
		d.log.Info("memd worker health serving", "addr", lis.Addr().String())
	}
	d.log.Info("memd worker running", "id", id, "slots", d.cfg.WorkerSlots)
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("memd: worker: %w", err)
	}
	return nil
}
