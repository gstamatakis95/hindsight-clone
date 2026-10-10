package mentalmodel

import (
	"context"
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
)

// env is one test's world: the fake store, clock and LLM, the stub reads the ports lack (CCR WP-12-1),
// and stub recall / reflect engines that return the in-scope units of their window.
type env struct {
	t      *testing.T
	ctx    context.Context
	bank   port.BankRef
	clock  *fake.Clock
	store  *fake.Store
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
	clock := fake.NewClock(time.Time{})
	store := fake.NewStore(clock)
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
		store: store, llm: fake.NewLLM(), emb: fake.NewEmbedder(fake.EmbedSimilar),
		config: config.NewResolver(store, nil, server),
	}
	e.reads = &stubReads{store: store}
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

// stubReads implements ScopeReader, ModelLister, CronSource and FailureSource over the fake store for the
// units and models a test created (the ports have no such reads yet).
type stubReads struct {
	store  *fake.Store
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

// matching filters units by types, tags and seq.
func matching(us []domain.Unit, types []domain.FactType, tags *port.SQLPredicate, r port.SeqRange) []domain.Unit {
	var out []domain.Unit
	for _, u := range us {
		if (len(types) == 0 || slices.Contains(types, u.Type)) && tags.Eval(u.Tags) && r.Contains(u.Seq) {
			out = append(out, u)
		}
	}
	return out
}

// NewestSeq implements ScopeReader.
func (s *stubReads) NewestSeq(ctx context.Context, q ScopeQuery) (int64, error) {
	var newest int64
	for _, u := range matching(s.liveUnits(ctx, q.Bank), q.Types, q.Tags, q.Seq) {
		newest = max(newest, u.Seq)
	}
	return newest, nil
}

// ListModels implements ModelLister (one page, ordered by id).
func (s *stubReads) ListModels(ctx context.Context, q ModelQuery) ([]domain.MentalModel, string, error) {
	s.mu.Lock()
	refs := slices.Clone(s.models)
	s.mu.Unlock()
	var out []domain.MentalModel
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		for _, r := range refs {
			if r.b != q.Bank {
				continue
			}
			m, err := tx.MentalModels().Get(ctx, r.b, r.id)
			if err != nil {
				continue
			}
			if keepModel(m, q) {
				out = append(out, m)
			}
		}
		return nil
	})
	slices.SortFunc(out, func(a, b domain.MentalModel) int {
		return strings.Compare(string(a.Spec.ID), string(b.Spec.ID))
	})
	return out, "", err //nolint:wrapcheck // test stub over the fake store
}

// keepModel applies a ModelQuery's filters.
func keepModel(m domain.MentalModel, q ModelQuery) bool {
	f := m.Spec.FolderPath
	if q.FolderPrefix != "" && f != q.FolderPrefix && !strings.HasPrefix(f, q.FolderPrefix+"/") {
		return false
	}
	if q.RefreshAfterConsolidation && !m.Spec.Trigger.RefreshAfterConsolidation {
		return false
	}
	return len(q.Tags) == 0 || domain.MatchTags(m.Spec.Tags, q.Tags, q.TagsMatch)
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
