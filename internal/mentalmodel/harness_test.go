package mentalmodel

import (
	"context"
	"encoding/json"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	mockai "memd/internal/mocks/ai"
	"memd/internal/prompts"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	"memd/internal/reflect"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sameVector is the embedding of every unit, page and query: recall returns every live unit that passes its
// tag, type and seq filters.
var sameVector = func() []float32 {
	v := make([]float32, domain.EmbeddingDim)
	v[0] = 1
	return v
}()

// env is one test's world: the Service over its own database with a real recall engine and reflect agent, the
// fake clock and LLM (the REFLECT slot answered by testLLM), an embedder that counts query embeddings (one per
// retrieval) and test-set cron and failed-refresh lists.
type env struct {
	t       *testing.T
	ctx     context.Context
	bank    domain.BankRef
	clock   *clock.Manual
	store   *postgres.DB
	pool    *pgxpool.Pool
	llm     *testLLM
	emb     *mockai.Embedder
	queries atomic.Int32
	maint   *stubMaint
	config  *config.Resolver
	recall  *recall.Engine
	agent   *reflect.Agent
	svc     *Service
}

// newEnv builds an env with every LLM slot bound to "test-model" and the bank created.
func newEnv(t *testing.T) *env {
	t.Helper()
	// The test clock runs an hour ahead of the database, so every run_at the service parks is in the
	// database's future until claimDue releases the ones the test clock says are due.
	clk := clock.NewManual(time.Now().Add(time.Hour).UTC().Truncate(time.Second))
	db := pgtest.New(t)
	pg := db.Store()
	server, err := config.ServerDefaults(domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "test-model"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ai.NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, ctx: context.Background(), bank: domain.BankRef{Tenant: "t1", Bank: "b1"}, clock: clk,
		store: pg, pool: db.Pool, llm: &testLLM{LLM: fake.NewLLM()}, config: config.NewResolver(pg, server),
		maint: &stubMaint{Maintenance: pg},
	}
	e.emb = &mockai.Embedder{
		EmbedQueryFunc: func(context.Context, string) ([]float32, error) {
			e.queries.Add(1)
			return sameVector, nil
		},
		EmbedDocumentsFunc: func(_ context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i := range out {
				out[i] = sameVector
			}
			return out, nil
		},
	}
	router := ai.NewRouter()
	r := pg.Read()
	e.recall = recall.New(recall.Deps{
		DB: pg, Config: e.config, Embedder: e.emb, Router: router, Clock: clk, Tokens: tok,
		Arms: []recall.Arm{arms.NewVector(r), arms.NewText(r), arms.NewGraph(r, pg.Graph()), arms.NewTemporal(r)},
	})
	e.agent = reflect.New(reflect.Deps{DB: pg, Config: e.config, Router: router, LLM: e.llm, Clock: clk, Recall: e.recall})
	e.svc = e.service(nil)
	e.tx(func(tx store.Tx) error {
		_, err := tx.Banks().Ensure(e.ctx, e.bank)
		return err
	})
	return e
}

// service builds a Service over e's dependencies, adjusted by mod.
func (e *env) service(mod func(*Deps)) *Service {
	d := Deps{
		DB: e.store, Maintenance: e.maint, Config: e.config, Recall: e.recall, Reflect: e.agent, LLM: e.llm,
		Router: ai.NewRouter(), Embedder: e.emb, Clock: e.clock,
	}
	if mod != nil {
		mod(&d)
	}
	return New(d)
}

// claimDue claims up to 10 jobs of kind that are due by the test clock: the database decides due-ness by
// its own clock, so pending jobs whose run_at the test clock has reached are made due first.
func (e *env) claimDue(kind domain.OperationKind) []store.Job {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, `UPDATE operations SET run_at = now()
		WHERE status = 'pending' AND run_at > now() AND run_at <= $1`, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
	jobs, err := e.store.Claim(e.ctx, "w1", []domain.OperationKind{kind}, 10)
	if err != nil {
		e.t.Fatal(err)
	}
	return jobs
}

// opID is the operation id of a readable name (Postgres stores operation ids as uuid).
func opID(name string) domain.OperationID { return pgtest.ID[domain.OperationID](name) }

// tx runs fn in a transaction and fails the test on error.
func (e *env) tx(fn func(store.Tx) error) {
	e.t.Helper()
	if err := e.store.InTx(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
}

// addUnit commits one embedded unit with its own change seq and returns it.
func (e *env) addUnit(typ domain.FactType, text string, tags ...string) domain.Unit {
	e.t.Helper()
	u := domain.Unit{
		ID: domain.UnitID(uuid.NewString()), Bank: e.bank, Type: typ, Text: text, Tags: domain.NormalizeTags(tags),
		Embedding: sameVector,
	}
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, e.bank)
		if err != nil {
			return err
		}
		u.Seq = seq
		return tx.Units().Insert(e.ctx, []domain.Unit{u})
	})
	return u
}

// archive invalidates a unit.
func (e *env) archive(id domain.UnitID) {
	e.t.Helper()
	e.tx(func(tx store.Tx) error {
		if _, err := tx.Seq(e.ctx, e.bank); err != nil {
			return err
		}
		return tx.Units().Archive(e.ctx, e.bank, id, "test")
	})
}

// addModel stores a model directly (no build enqueued).
func (e *env) addModel(spec domain.MentalModelSpec) domain.MentalModel {
	e.t.Helper()
	e.tx(func(tx store.Tx) error {
		return tx.MentalModels().Create(e.ctx, domain.MentalModel{Spec: spec, Bank: e.bank})
	})
	return e.model(spec.ID)
}

// model reads a model back.
func (e *env) model(id domain.MentalModelID) domain.MentalModel {
	e.t.Helper()
	var m domain.MentalModel
	e.tx(func(tx store.Tx) error {
		var err error
		m, err = tx.MentalModels().Get(e.ctx, e.bank, id)
		return err
	})
	return m
}

// history reads a model's history, newest first.
func (e *env) history(id domain.MentalModelID, kind domain.HistoryKind) []domain.MentalModelHistoryEntry {
	e.t.Helper()
	var h []domain.MentalModelHistoryEntry
	e.tx(func(tx store.Tx) error {
		var err error
		h, err = tx.MentalModels().History(e.ctx, e.bank, id, kind, 0)
		return err
	})
	return h
}

// llmCalls counts the LLM calls made so far (the reflect loop's included).
func (e *env) llmCalls() int { return len(e.llm.Calls()) + e.llm.reflectCalls() }

// retrievals counts the recalls made so far (each embeds its query once).
func (e *env) retrievals() int { return int(e.queries.Load()) }

// scriptSynthesize scripts the synthesize reply.
func (e *env) scriptSynthesize(content string) {
	e.llm.RespondChat(domain.ModelSlotMentalModel, prompts.SynthesizeSystem,
		ai.ChatResponse{Message: ai.ChatMessage{Content: content}})
}

// stubMaint is the store's store.Maintenance with test-set CronModels and FailedRefreshes lists.
type stubMaint struct {
	store.Maintenance
	cron   []store.CronModel
	failed []store.FailedRefresh
}

// CronModels returns the test's list.
func (s *stubMaint) CronModels(context.Context) ([]store.CronModel, error) { return s.cron, nil }

// FailedRefreshes returns the test's list.
func (s *stubMaint) FailedRefreshes(context.Context) ([]store.FailedRefresh, error) {
	return s.failed, nil
}

// testLLM is the fake LLM with the REFLECT slot answered like a model that drives the tools: its first turn
// calls recall, its next calls done with a document listing every memory recall returned and citing them (or
// citing none when citeNothing is set).
type testLLM struct {
	*fake.LLM
	mu          sync.Mutex
	reflects    []ai.ChatRequest
	citeNothing bool
}

// Chat answers REFLECT turns itself and passes every other call to the fake.
func (l *testLLM) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	if req.Slot != domain.ModelSlotReflect {
		return l.LLM.Chat(ctx, req) //nolint:wrapcheck // test pass-through
	}
	l.mu.Lock()
	l.reflects = append(l.reflects, req)
	cite := !l.citeNothing
	l.mu.Unlock()
	var ids, lines []string
	sawTool := false
	for _, m := range req.Messages {
		if m.Role != ai.ChatRoleTool {
			continue
		}
		sawTool = true
		var out struct {
			Memories []struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			} `json:"memories"`
		}
		_ = json.Unmarshal([]byte(m.Content), &out)
		for _, mem := range out.Memories {
			ids, lines = append(ids, mem.ID), append(lines, "- "+mem.Text)
		}
	}
	if !sawTool {
		return toolTurn(reflect.ToolRecall, map[string]any{"reason": "r", "query": "status"}), nil
	}
	if !cite {
		ids, lines = nil, []string{"Something uncited."}
	}
	doc := map[string]any{"sections": []map[string]any{
		{"heading": "Summary", "level": 2, "blocks": []string{strings.Join(lines, "\n")}},
	}}
	return toolTurn(reflect.ToolDone, map[string]any{"document": doc, "memory_ids": ids}), nil
}

// reflectCalls counts the REFLECT turns.
func (l *testLLM) reflectCalls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reflects)
}

// firstReflect returns the system message of REFLECT turn i.
func (l *testLLM) reflectSystem(i int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i >= len(l.reflects) || len(l.reflects[i].Messages) == 0 {
		return ""
	}
	return l.reflects[i].Messages[0].Content
}

// toolTurn is an assistant turn calling one tool with args.
func toolTurn(name string, args map[string]any) ai.ChatResponse {
	raw, _ := json.Marshal(args)
	return ai.ChatResponse{Message: ai.ChatMessage{ToolCalls: []ai.ToolCall{
		{ID: "c-" + name, Name: name, Arguments: raw},
	}}}
}

// fakeJSON is a scripted Structured reply.
func fakeJSON(body string) fake.Reply { return fake.Reply{JSON: body} }
