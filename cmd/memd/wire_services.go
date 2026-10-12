package main

import (
	"log/slog"
	"memd/internal/ai"
	"memd/internal/bank"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/configsvc"
	"memd/internal/document"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/grpcapi"
	"memd/internal/linking"
	"memd/internal/maintenance"
	"memd/internal/memory"
	"memd/internal/mentalmodel"
	"memd/internal/observability"
	"memd/internal/observation"
	"memd/internal/operation"
	"memd/internal/org"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	"memd/internal/reflect"
	"memd/internal/store"
	"memd/internal/timex"
	"net/http"
	"time"
)

// core are the libraries several services share.
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

// wireServices builds the shared libraries, then every service over them; tel (nil = Datadog off) traces the
// gRPC server, the jobs and the maintenance tick.
func wireServices(
	cfg Config, in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock, log *slog.Logger,
	tel *observability.Telemetry,
) *deps {
	c := wireCore(in, tok, serverCfg, clk)
	d := &deps{
		cfg: cfg, log: log, claimer: in.claimer, db: in.db, clock: clk, tel: tel, handlers: jobHandlers{},
		recallArms: armNames(c.recall),
		probes: []grpcapi.ReadinessProbe{
			grpcapi.PostgresProbe{Ping: in.ping}, grpcapi.NewGatewayProbe(cfg.GatewayURL, &http.Client{}, clk),
		},
	}
	wireMemory(c, in, d)
	wireDocuments(c, in, d)
	wireObservations(c, in, d)
	wireMentalModels(c, in, d)
	wireOrg(c, d)
	wireBanks(c, in, d)
	wireConfig(c, d)
	wireOperations(c, d)
	wireMaintenance(cfg, c, in, d)
	return d
}

// recallArms are the registered recall arms, in fusion order: the built-in vector, text, graph and temporal arms.
// A new arm implements recall.Arm in its own file under internal/recall/arms (or its own package), takes only the
// boundary it reads, and is added here.
func recallArms(s store.Search, g graph.Reader) []recall.Arm {
	return []recall.Arm{arms.NewVector(s), arms.NewText(s), arms.NewGraph(s, g), arms.NewTemporal(s)}
}

// armNames are the names of e's registered arms.
func armNames(e *recall.Engine) []string {
	var out []string
	for _, a := range e.Arms() {
		out = append(out, a.Name)
	}
	return out
}

// wireCore builds the libraries: config resolution, routing, time normalization, linking, recall (the registered
// arms over the store and the graph reader) and the reflect agent.
func wireCore(in infra, tok *ai.Tokenizer, serverCfg domain.BankConfig, clk clock.Clock) core {
	db, gw := in.db, in.gw
	c := core{db: db, read: db.Read(), clock: clk, tok: tok, router: ai.NewRouter()}
	c.resolver = config.NewResolver(db, serverCfg)
	c.times = timex.New(gw, c.router, clk)
	c.linker = linking.NewLinker()
	c.entities = linking.NewResolver(linking.ResolverConfig{})
	c.recall = recall.New(recall.Deps{
		DB: db, Arms: recallArms(c.read, in.graph), Config: c.resolver, Embedder: gw, Reranker: gw, Router: c.router,
		Times: c.times, Clock: clk, Tokens: tok,
	})
	c.agent = reflect.New(reflect.Deps{
		DB: db, Config: c.resolver, Router: c.router, LLM: gw, Clock: clk, Recall: c.recall,
	})
	return c
}

// wireMemory builds the memory area: memory.Service serves MemoryService (retain, recall, reflect, listing and
// curation), runs the retain_document and rederive_memory jobs, and provides the maintenance tick's
// rederive_stranded and settle_retain_batches steps.
func wireMemory(c core, in infra, d *deps) {
	svc := memory.New(memory.Deps{
		DB: c.db, Maintenance: in.maint, Blob: in.blob, LLM: in.gw, Embedder: in.gw, Router: c.router,
		Tokens: c.tok, Config: c.resolver, Times: c.times, Linker: c.linker, Entities: c.entities,
		Recall: c.recall, Agent: c.agent, Clock: c.clock,
	})
	d.api.Memory = &grpcapi.MemoryHandler{Memory: svc}
	d.handlers.add(domain.OperationKindRetainDocument, svc.RunRetainJob)
	d.handlers.add(domain.OperationKindRederiveMemory, svc.RunRederiveJob)
	d.maintenance.Memory = svc
}

// wireDocuments builds document.Service: it serves DocumentService (the document reads, retag and reprocess)
// and runs the retag_document job.
func wireDocuments(c core, in infra, d *deps) {
	svc := document.New(document.Deps{DB: c.db, Blob: in.blob, Clock: c.clock})
	d.api.Document = &grpcapi.DocumentHandler{Documents: svc}
	d.handlers.add(domain.OperationKindRetagDocument, svc.RunRetagJob)
}

// wireObservations builds observation.Service: it serves ObservationService, runs the consolidate job and the
// tick's consolidation reconcile sweep.
func wireObservations(c core, in infra, d *deps) {
	svc := observation.New(observation.Deps{
		DB: c.db, Maintenance: in.maint, Config: c.resolver, Router: c.router, LLM: in.gw, Embedder: in.gw,
		Tokens: c.tok, Clock: c.clock,
	})
	d.api.Observation = &grpcapi.ObservationHandler{Observations: svc}
	d.handlers.add(domain.OperationKindConsolidate, svc.RunConsolidateJob)
	d.maintenance.Observations = svc
}

// wireMentalModels builds mentalmodel.Service: it serves MentalModelService, runs the refresh_mental_model job and
// the tick's failed-refresh and cron sweeps.
func wireMentalModels(c core, in infra, d *deps) {
	svc := mentalmodel.New(mentalmodel.Deps{
		DB: c.db, Maintenance: in.maint, Config: c.resolver, Recall: c.recall, Reflect: c.agent, LLM: in.gw,
		Router: c.router, Embedder: in.gw, Clock: c.clock,
	})
	d.api.MentalModel = &grpcapi.MentalModelHandler{MentalModels: svc}
	d.handlers.add(domain.OperationKindRefreshMentalModel, svc.RunRefreshJob)
	d.maintenance.MentalModels = svc
}

// wireOrg builds org.Service: it serves OrgService (create, get, list; DeleteOrg answers UNIMPLEMENTED).
func wireOrg(c core, d *deps) {
	d.api.Org = &grpcapi.OrgHandler{Orgs: org.New(org.Deps{DB: c.db})}
}

// wireBanks builds bank.Service: it serves BankService (create, get, list, delete, stats, tags, templates), runs the
// delete_bank job and the tick's resume_bank_deletes sweep.
func wireBanks(c core, in infra, d *deps) {
	svc := bank.New(bank.Deps{DB: c.db, Maintenance: in.maint, Blob: in.blob, Clock: c.clock})
	d.api.Bank = &grpcapi.BankHandler{Banks: svc}
	d.handlers.add(domain.OperationKindDeleteBank, svc.RunDeleteJob)
	d.maintenance.Banks = svc
}

// wireConfig builds configsvc.Service: it serves ConfigService (the tenant, org and bank config levels, the prompt
// preview and the directives).
func wireConfig(c core, d *deps) {
	svc := configsvc.New(configsvc.Deps{DB: c.db, Config: c.resolver})
	d.api.Config = &grpcapi.ConfigHandler{Config: svc}
}

// wireOperations builds operation.Service: it serves OperationService (operation and event reads, cancel, retry).
func wireOperations(c core, d *deps) {
	d.api.Operation = &grpcapi.OperationHandler{Operations: operation.New(operation.Deps{DB: c.db})}
}

// wireMaintenance builds maintenance.Service, which runs the tick and the graph_maintenance job. It runs last:
// wireBanks, wireMemory, wireObservations and wireMentalModels set their services' tick steps in d.maintenance
// first. The cross-bank scans come from store.Maintenance, and the operator settings from the server config file's
// worker and retention sections.
func wireMaintenance(cfg Config, c core, in infra, d *deps) {
	m := &d.maintenance
	m.Claimer, m.Store, m.Clock, m.Logger = in.claimer, c.db, c.clock, d.log
	m.Graph = in.maint
	m.Linker = c.linker
	m.TraceTick = d.tel.TickHook()
	f := cfg.File
	m.Config = maintenance.Config{
		Interval: time.Duration(f.Worker.MaintenanceInterval), StaleAfter: time.Duration(f.Worker.StaleAfter),
		OperationRetention: time.Duration(f.Retention.Operations), EventRetention: time.Duration(f.Retention.Events),
	}
	d.maint = maintenance.New(*m)
	d.handlers.add(domain.OperationKindGraphMaintenance, d.maint.RunGraphJob)
}
