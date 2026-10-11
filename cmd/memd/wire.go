package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/adapter/gateway"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/s3"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"
	"memd/internal/engine/docs"
	"memd/internal/engine/maintenance"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/recall"
	"memd/internal/engine/reflect"
	"memd/internal/engine/retain"
	"memd/internal/engine/routing"
	"memd/internal/engine/timex"
	"memd/internal/port"
	"memd/internal/server"
	"memd/internal/worker"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/reflection"
)

// wire.go is the only composition root: it builds the adapters (Postgres, S3, gateway), narrows each to the
// ports the engines consume, builds every engine once, and hands them to the gRPC server (api mode) and the
// job handlers and maintenance tick (worker mode).

// modelGateway is the AI gateway as the engines see it: one client serves all three model ports.
type modelGateway interface {
	port.LLM
	port.Embedder
	port.Reranker
}

// ports are the adapters narrowed to the interfaces the engines, handlers and probes consume. openAdapters
// is the only place the concrete adapter types appear; tests build ports from internal/fake.
type ports struct {
	store    port.Store
	claimer  port.Claimer
	blob     port.Blob
	gw       modelGateway
	db       pinger
	stranded worker.StrandedFactSource
	batches  worker.BatchSettler
	maint    maintenanceReads
}

// maintenanceReads are the maintenance tick's reads the ports don't expose; the Postgres store implements
// them (decisions 2000-2002).
type maintenanceReads interface {
	worker.ConsolidationCandidates
	worker.StalledDeleteSource
	worker.GraphWorkSource
}

// deps is everything the two modes run.
type deps struct {
	cfg         Config
	log         *slog.Logger
	server      server.Deps
	handlers    worker.Handlers
	maintenance worker.MaintenanceDeps
	claimer     port.Claimer
	pending     worker.PendingChecker
	clock       port.Clock
}

// buildDeps connects the adapters, checks the gateway's embedding dimension and builds every engine. The
// returned closer releases the Postgres pool.
func buildDeps(ctx context.Context, cfg Config, log *slog.Logger) (*deps, func(), error) {
	p, closer, err := openAdapters(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := checkEmbedding(ctx, p.gw); err != nil {
		closer()
		return nil, nil, err
	}
	tok, err := newTokenCounter()
	if err != nil {
		closer()
		return nil, nil, err
	}
	serverCfg, err := config.ServerDefaults(cfg.Env)
	if err != nil {
		closer()
		return nil, nil, fmt.Errorf("memd: %w", err)
	}
	d := wireEngines(cfg, p, tok, serverCfg, systemClock{}, log)
	return d, closer, nil
}

// openAdapters opens the Postgres pool, the blob store and the gateway client, and narrows them to ports.
func openAdapters(ctx context.Context, cfg Config) (ports, func(), error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return ports{}, nil, fmt.Errorf("memd: %s: %w", envDSN, err)
	}
	pcfg.MaxConns = int32(poolSize(cfg)) //nolint:gosec // poolSize is small and positive
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return ports{}, nil, fmt.Errorf("memd: postgres: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return ports{}, nil, fmt.Errorf("memd: postgres: %w", err)
	}
	blob, err := s3.NewBlob(ctx, s3.Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, AccessKeyID: cfg.S3AccessKey,
		SecretAccessKey: cfg.S3SecretKey, UsePathStyle: cfg.S3PathStyle,
	})
	if err != nil {
		pool.Close()
		return ports{}, nil, fmt.Errorf("memd: s3: %w", err)
	}
	gw := gateway.NewClient(gateway.Config{
		BaseURL: cfg.GatewayURL, APIKey: cfg.GatewayAPIKey, EmbedModel: cfg.EmbedModel,
		MaxConcurrent: cfg.GatewayMaxConcurrent, Timeout: cfg.GatewayTimeout,
	}, nil)
	store := postgres.New(pool)
	return ports{
		store: store, claimer: store, blob: blob, gw: gw, db: pool, stranded: store, batches: store,
		maint: store,
	}, pool.Close, nil
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
func checkEmbedding(ctx context.Context, emb port.Embedder) error {
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

// core are the engines several others depend on.
type core struct {
	store     port.Store
	read      port.Reader
	clock     port.Clock
	resolver  port.ConfigResolver
	router    port.ModelRouter
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

// wireEngines builds every engine over the adapters, narrowed to ports.
func wireEngines(
	cfg Config, p ports, tok *tokenCounter, serverCfg domain.BankConfig, clock port.Clock, log *slog.Logger,
) *deps {
	store, claimer := p.store, p.claimer
	c := wireCore(p, tok, serverCfg, clock)
	mm := wireMentalModels(c, p.gw)
	d := &deps{cfg: cfg, log: log, claimer: claimer, clock: clock, pending: worker.NewPendingChecker(store, clock)}
	d.server = wireServer(cfg, c, mm, p)
	d.handlers = worker.Handlers{
		Retainer: c.retainer,
		Consolidator: consolidate.NewConsolidator(consolidate.ConsolidatorDeps{
			Store: store, Config: c.resolver, Router: c.router, LLM: p.gw, Embedder: p.gw, Clock: clock,
			Scopes: c.scopes, Tokens: tok,
			Checkpoint: func(j port.Job) consolidate.Checkpointer { return worker.NewCheckpoint(claimer, j.ID) },
		}),
		Refresher:  mm.runner,
		Deleter:    d.server.Deleter,
		Rederiver:  curate.NewRederiver(curate.RederiverDeps{Store: store, Embedder: p.gw, Linker: c.linker}),
		Retagger:   d.server.Retagger,
		Graph:      maintenance.NewGraphMaintainer(store, c.linker, clock),
		Store:      store,
		Clock:      clock,
		Checkpoint: func(j port.Job) worker.Checkpoint { return worker.NewCheckpoint(claimer, j.ID) },
	}
	d.maintenance = worker.MaintenanceDeps{
		Claimer: claimer, Store: store, Clock: clock, CronRefresh: mm.triggers,
		Consolidation: p.maint, Config: c.resolver, ConsolidationGrace: cfg.ConsolidationGrace,
		Deletes: p.maint, Graph: p.maint,
		RefreshFailures: mentalmodel.NewRefreshFailureReconciler(c.read, store, c.resolver, clock),
		Stranded:        p.stranded, Batches: p.batches, Interval: cfg.MaintenanceInterval, StaleAfter: cfg.StaleAfter,
		Logger: log,
	}
	return d
}

// wireCore builds config resolution, routing, retain, recall and reflect.
func wireCore(p ports, tok *tokenCounter, serverCfg domain.BankConfig, clock port.Clock) core {
	store, claimer, blob, gw := p.store, p.claimer, p.blob, p.gw
	c := core{store: store, read: store.Read(), clock: clock, router: routing.NewRouter()}
	c.matcher = consolidate.NewStrategyMatcher(c.read)
	c.resolver = config.NewResolver(store, c.matcher, serverCfg)
	c.times = timex.NewNormalizer(gw, c.router, clock)
	c.scopes = consolidate.NewScopeExpander()
	c.linker = retain.NewLinker()
	c.entities = retain.NewEntityResolver(retain.ResolverConfig{})
	c.retainer = retain.NewRetainer(retain.RetainerDeps{
		Store: store, Blob: blob, Config: c.resolver, Extractor: retain.NewExtractor(gw, c.router),
		Dates: c.times, Embedder: gw, Entities: c.entities, Labels: retain.NewLabelResolver(), Linker: c.linker,
		Scopes: c.scopes, Tokens: tok, Clock: clock,
		Checkpoint: func(id port.OperationID) retain.Checkpoint { return worker.NewCheckpoint(claimer, id) },
	})
	c.recaller = recall.NewRecaller(recall.RecallerDeps{
		Reader: c.read, Config: c.resolver, Embedder: gw, Reranker: gw, Router: c.router, Times: c.times,
		Clock: clock, Count: tok,
	})
	c.queuer = mentalmodel.NewRefreshQueuer(clock)
	c.dirs = bank.NewDirectiveSelector(store)
	pages := &pageReader{search: mentalmodel.NewPageSearcher(c.read, gw), s: store}
	c.reflector = reflect.NewReflector(reflect.ReflectorDeps{
		Store: store, Config: c.resolver, Router: c.router, LLM: gw, Clock: clock,
		Tools: reflect.DefaultTools(c.recaller, pages, c.read),
	})
	return c
}

// mentalModels are the mental-model engines.
type mentalModels struct {
	runner   mentalmodel.RefreshRunner
	admin    mentalmodel.Admin
	pages    mentalmodel.PageSearcher
	triggers mentalmodel.Triggers
}

// wireMentalModels builds the refresh runner (both strategies), admin, page search and triggers.
func wireMentalModels(c core, gw modelGateway) mentalModels {
	detector := mentalmodel.NewChangeDetector(c.read, c.read)
	md := mentalmodel.ModelDeps{LLM: gw, Router: c.router, Clock: c.clock, Directives: c.dirs}
	runner := mentalmodel.NewRefreshRunner(mentalmodel.RefreshRunnerDeps{
		Store: c.store, Config: c.resolver, Scope: c.read,
		Synthesizers: []mentalmodel.Synthesizer{
			mentalmodel.NewAgenticSynthesizer(c.reflector, md), mentalmodel.NewSingleShotSynthesizer(c.recaller, md),
		},
		Projector: reflect.NewStructuredProjector(gw, c.router, c.resolver), Embedder: gw, Clock: c.clock,
	})
	return mentalModels{
		runner: runner,
		admin: mentalmodel.NewAdmin(mentalmodel.AdminDeps{
			Store: c.store, Models: c.read, Detector: detector, Clock: c.clock, Config: c.resolver,
		}),
		pages: mentalmodel.NewPageSearcher(c.read, gw),
		triggers: mentalmodel.NewTriggers(mentalmodel.TriggerDeps{
			Store: c.store, Scheduler: mentalmodel.NewRefreshScheduler(detector, c.resolver), Models: c.read,
			Cron: c.read, Clock: c.clock,
		}),
	}
}

// wireServer fills server.Deps.
func wireServer(cfg Config, c core, mm mentalModels, p ports) server.Deps {
	blob := p.blob
	return server.Deps{
		Ingestor: retain.NewIngestor(retain.IngestorDeps{
			Store: c.store, Blob: blob, Config: c.resolver, Retainer: c.retainer, Merge: config.Merge,
			Scopes: c.scopes, Clock: c.clock,
		}),
		Recaller:  c.recaller,
		Reflector: c.reflector,
		Lister:    curate.NewMemoryLister(c.read),
		Curator: curate.NewCurator(curate.CuratorDeps{
			Store: c.store, Clock: c.clock, Citing: c.read, Refresh: c.queuer, Resolver: c.entities,
			Config: c.resolver, Archived: c.read,
		}),
		Stats:     bank.NewStatsReader(c.read),
		Previewer: bank.NewPromptPreviewer(c.resolver, c.dirs),
		Deleter: bank.NewBankDeleter(c.store, blob, p.claimer, c.clock,
			bank.DeleterConfig{Blobs: blob}),
		Templates:  bank.NewTemplateCodec(c.store, c.queuer),
		Retagger:   docs.NewRetagger(c.store),
		Strategies: c.matcher,
		Refresher:  mm.runner,
		Pages:      mm.pages,
		Control:    worker.NewOperationControl(c.store),
		Config:     c.resolver,
		Tenants:    bank.NewTenantAdmin(c.store),
		Banks:      bank.NewBankAdmin(c.store),
		Directives: bank.NewDirectiveAdmin(c.store),
		Documents: docs.NewDocumentAdmin(docs.DocumentAdminDeps{
			Store: c.store, Blob: blob, Source: c.read, Reprocessor: retain.NewReprocessor(c.store),
		}),
		Observations: curate.NewObservationAdmin(curate.ObservationAdminDeps{
			Store: c.store, Scopes: c.read, Clearer: consolidate.NewInvalidator(),
		}),
		MentalModels:      mm.admin,
		Operations:        &operationReader{s: c.store},
		ValidateOverrides: config.ValidateOverrides,
		DefaultDeadline:   cfg.RPCDeadline,
		Probes: []server.ReadinessProbe{
			postgresProbe{db: p.db}, &gatewayProbe{url: cfg.GatewayURL, hc: &http.Client{}},
		},
	}
}

// apiShutdownGrace is how long in-flight RPCs may finish after a stop signal.
const apiShutdownGrace = 20 * time.Second

// runAPI serves gRPC on cfg.GRPCAddr until ctx is done, then stops gracefully.
func runAPI(ctx context.Context, d *deps) error {
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("memd: listen %s: %w", d.cfg.GRPCAddr, err)
	}
	srv := server.New(d.server)
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

// runWorker runs the job runner and the maintenance tick until ctx is done. The runner's StaleAfter and
// the maintenance reclaim's StaleAfter come from the one config value, so the runner's self-fence (3/4 of
// it) always fires before another worker can reclaim the job.
func runWorker(ctx context.Context, d *deps) error {
	r := worker.NewRunner(d.claimer, worker.RunnerConfig{
		WorkerID: d.cfg.WorkerID, Clock: d.clock, Slots: d.cfg.WorkerSlots, PollInterval: d.cfg.PollInterval,
		HeartbeatInterval: d.cfg.HeartbeatInterval, StaleAfter: d.cfg.StaleAfter, Pending: d.pending, Logger: d.log,
	})
	worker.Register(r, d.handlers)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return r.Run(gctx) })
	g.Go(func() error { return worker.RunMaintenance(gctx, d.maintenance) })
	d.log.Info("memd worker running", "slots", d.cfg.WorkerSlots)
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("memd: worker: %w", err)
	}
	return nil
}
