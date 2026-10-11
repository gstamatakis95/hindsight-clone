package mentalmodel

import (
	"context"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/engine/reflect"
	"memd/internal/engine/routing"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// env is one test's world: a store over its own database, the fake clock and LLM, its reads (stubReads),
// and stub recall / reflect engines that return the in-scope units of their window.
type env struct {
	t      *testing.T
	ctx    context.Context
	bank   port.BankRef
	clock  *fake.Clock
	store  *postgres.Store
	pool   *pgxpool.Pool
	llm    *fake.LLM
	emb    *fake.Embedder
	reads  *stubReads
	config port.ConfigResolver
	rc     *stubRecaller
	rf     *stubReflector
	runner RefreshRunner
}

// newEnv builds an env with every LLM slot bound to "test-model" and the bank created.
func newEnv(t *testing.T) *env {
	t.Helper()
	// The test clock runs an hour ahead of the database, so every run_at the engines park is in the
	// database's future until claimDue releases the ones the test clock says are due.
	clock := fake.NewClock(time.Now().Add(time.Hour).UTC().Truncate(time.Second))
	db := pgtest.New(t)
	store := db.Store()
	server, err := config.ServerDefaults(func(k string) string {
		if k == config.EnvModel {
			return "test-model"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, ctx: context.Background(), bank: port.BankRef{Tenant: "t1", Bank: "b1"}, clock: clock,
		store: store, pool: db.Pool, llm: fake.NewLLM(), emb: fake.NewEmbedder(fake.EmbedSimilar),
		config: config.NewResolver(store, nil, server),
	}
	e.reads = &stubReads{Reader: store.Read(), store: store}
	e.rc = &stubRecaller{reads: e.reads}
	e.rf = &stubReflector{reads: e.reads}
	md := ModelDeps{LLM: e.llm, Router: routing.NewRouter(), Clock: clock}
	e.runner = NewRefreshRunner(RefreshRunnerDeps{
		Store: store, Config: e.config, Scope: e.reads, Clock: clock, Embedder: e.emb,
		Synthesizers: []Synthesizer{NewAgenticSynthesizer(e.rf, md), NewSingleShotSynthesizer(e.rc, md)},
	})
	e.tx(func(tx port.Tx) error {
		_, err := tx.Banks().Ensure(e.ctx, e.bank)
		return err
	})
	return e
}

// claimDue claims up to 10 jobs of kind that are due by the test clock: the database decides due-ness by
// its own clock, so pending jobs whose run_at the test clock has reached are made due first.
func (e *env) claimDue(kind port.JobKind) []port.Job {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, `UPDATE operations SET run_at = now()
		WHERE status = 'pending' AND run_at > now() AND run_at <= $1`, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
	jobs, err := e.store.Claim(e.ctx, "w1", []port.JobKind{kind}, 10)
	if err != nil {
		e.t.Fatal(err)
	}
	return jobs
}

// opID is the operation id of a readable name (Postgres stores operation ids as uuid).
func opID(name string) port.OperationID { return pgtest.ID[port.OperationID](name) }

// tx runs fn in a transaction and fails the test on error.
func (e *env) tx(fn func(port.Tx) error) {
	e.t.Helper()
	if err := e.store.InTx(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
}

// addUnit commits one unit with its own change seq and returns it.
func (e *env) addUnit(typ domain.FactType, text string, tags ...string) domain.Unit {
	e.t.Helper()
	u := domain.Unit{
		ID: port.UnitID(uuid.NewString()), Bank: e.bank, Type: typ, Text: text, Tags: domain.NormalizeTags(tags),
	}
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(e.ctx, e.bank)
		if err != nil {
			return err
		}
		u.Seq = seq
		return tx.Units().Insert(e.ctx, []domain.Unit{u})
	})
	e.reads.track(u.ID)
	return u
}

// archive invalidates a unit.
func (e *env) archive(id port.UnitID) {
	e.t.Helper()
	e.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(e.ctx, e.bank); err != nil {
			return err
		}
		return tx.Units().Archive(e.ctx, e.bank, id, "test")
	})
}

// addModel stores a model directly (no build enqueued) and tracks it for listing.
func (e *env) addModel(spec domain.MentalModelSpec) domain.MentalModel {
	e.t.Helper()
	e.tx(func(tx port.Tx) error {
		return tx.MentalModels().Create(e.ctx, domain.MentalModel{Spec: spec, Bank: e.bank})
	})
	e.reads.trackModel(e.bank, spec.ID)
	return e.model(spec.ID)
}

// model reads a model back.
func (e *env) model(id port.MentalModelID) domain.MentalModel {
	e.t.Helper()
	var m domain.MentalModel
	e.tx(func(tx port.Tx) error {
		var err error
		m, err = tx.MentalModels().Get(e.ctx, e.bank, id)
		return err
	})
	return m
}

// history reads a model's history, newest first.
func (e *env) history(id port.MentalModelID, kind domain.HistoryKind) []domain.MentalModelHistoryEntry {
	e.t.Helper()
	var h []domain.MentalModelHistoryEntry
	e.tx(func(tx port.Tx) error {
		var err error
		h, err = tx.MentalModels().History(e.ctx, e.bank, id, kind, 0)
		return err
	})
	return h
}

// llmCalls counts the LLM calls made so far.
func (e *env) llmCalls() int { return len(e.llm.Calls()) }

// scriptSynthesize scripts the SYNTHESIZE reply.
func (e *env) scriptSynthesize(content string) {
	e.llm.RespondChat(domain.ModelSlotSynthesize, "synthesize_system",
		port.ChatResponse{Message: port.ChatMessage{Content: content}})
}

// stubReads is the store's port.Reader (so NewestSeqInScope, TagCounts and ListMentalModels are the
// real reads) with test-only CronModels and FailedRefreshes lists, and it tracks the units a test
// created for the stub recall and reflect engines.
type stubReads struct {
	port.Reader
	store  *postgres.Store
	mu     sync.Mutex
	units  []port.UnitID
	models []modelRef
	cron   []CronModel
	failed []FailedRefresh
}

// modelRef names a tracked model.
type modelRef struct {
	b  port.BankRef
	id port.MentalModelID
}

func (s *stubReads) track(id port.UnitID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.units = append(s.units, id)
}

func (s *stubReads) trackModel(b port.BankRef, id port.MentalModelID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append(s.models, modelRef{b, id})
}

// liveUnits returns the tracked units of b that are still live.
func (s *stubReads) liveUnits(ctx context.Context, b port.BankRef) []domain.Unit {
	s.mu.Lock()
	ids := slices.Clone(s.units)
	s.mu.Unlock()
	us, err := s.store.Read().GetUnits(ctx, b, ids)
	if err != nil {
		panic(err)
	}
	return us
}

// CronModels implements CronSource.
func (s *stubReads) CronModels(context.Context) ([]CronModel, error) { return s.cron, nil }

// FailedRefreshes implements FailureSource.
func (s *stubReads) FailedRefreshes(context.Context) ([]FailedRefresh, error) { return s.failed, nil }

// stubRecaller returns the live in-scope units of the request's window (observations and facts).
type stubRecaller struct {
	reads *stubReads
	mu    sync.Mutex
	reqs  []recall.RecallRequest
}

// Recall implements Recaller.
func (r *stubRecaller) Recall(ctx context.Context, req recall.RecallRequest) (recall.RecallResult, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	var res recall.RecallResult
	for _, u := range r.reads.liveUnits(ctx, req.Bank) {
		if (len(req.Types) == 0 || slices.Contains(req.Types, u.Type)) && domain.MatchFilter(u.Tags, req.Tags) &&
			req.Seq.Contains(u.Seq) {
			res.Results = append(res.Results, recall.Memory{Unit: u})
		}
	}
	return res, nil
}

// stubReflector answers as a document listing the in-scope units of its window and cites them.
type stubReflector struct {
	reads *stubReads
	mu    sync.Mutex
	reqs  []reflect.ReflectRequest
	err   error
}

// Reflect implements Reflector.
func (r *stubReflector) Reflect(ctx context.Context, req reflect.ReflectRequest) (reflect.ReflectResult, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return reflect.ReflectResult{}, err
	}
	res := reflect.ReflectResult{Usage: []domain.Usage{{Slot: domain.ModelSlotReflect, Model: "test-model"}}}
	lines := []string{"## Summary"}
	for _, u := range r.reads.liveUnits(ctx, req.Bank) {
		if domain.MatchFilter(u.Tags, req.Tags) && req.Seq.Contains(u.Seq) {
			res.BasedOn.Memories = append(res.BasedOn.Memories, u)
			lines = append(lines, "- "+u.Text)
		}
	}
	if len(res.BasedOn.Memories) > 0 {
		res.Text = strings.Join(lines, "\n")
	}
	return res, nil
}

// fakeJSON is a scripted Structured reply.
func fakeJSON(body string) fake.Reply { return fake.Reply{JSON: body} }

// routingRouter is the production model router.
func routingRouter() port.ModelRouter { return routing.NewRouter() }
