package retain

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/timex"
	"memd/internal/fake"
	"memd/internal/prompts"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"strings"
	"testing"
	"time"
)

// testBank is the bank every retain test writes to.
var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// env is a retain engine over the in-memory fakes.
type env struct {
	t        *testing.T
	ctx      context.Context
	clock    *clock.Manual
	store    *postgres.DB
	llm      *fake.LLM
	emb      *fake.Embedder
	blob     *fake.Blob
	cfg      config.Resolver
	retainer Retainer
	ingestor Ingestor
}

// newEnv builds the engine; batch is RetainerDeps.BatchChunks (0 = default).
func newEnv(t *testing.T, batch int) *env {
	t.Helper()
	server, err := config.ServerDefaults(func(k string) string {
		if k == ai.EnvModel {
			return "test-model"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, ctx: context.Background(), clock: clock.NewManual(time.Time{}), llm: fake.NewLLM(),
		emb: fake.NewEmbedder(fake.EmbedSimilar), blob: fake.NewBlob(),
	}
	e.store = pgtest.NewStore(t)
	e.cfg = config.NewResolver(e.store, nil, server)
	router := ai.NewRouter()
	e.retainer = NewRetainer(RetainerDeps{
		Store: e.store, Blob: e.blob, Config: e.cfg, Extractor: NewExtractor(e.llm, router),
		Dates: timex.NewNormalizer(e.llm, router, e.clock), Embedder: e.emb,
		Entities: NewEntityResolver(ResolverConfig{}), Labels: NewLabelResolver(), Linker: NewLinker(),
		Clock: e.clock, BatchChunks: batch,
	})
	e.ingestor = NewIngestor(IngestorDeps{
		Store: e.store, Blob: e.blob, Config: e.cfg, Retainer: e.retainer, Merge: config.Merge, Clock: e.clock,
	})
	return e
}

// jsonFact is one scripted extraction fact (the concise / verbose / custom schema).
type jsonFact struct {
	What           string         `json:"what"`
	When           string         `json:"when"`
	Where          string         `json:"where"`
	Who            string         `json:"who"`
	Why            string         `json:"why"`
	FactKind       string         `json:"fact_kind"`
	FactType       string         `json:"fact_type"`
	OccurredStart  *string        `json:"occurred_start"`
	OccurredEnd    *string        `json:"occurred_end"`
	TimeExpression *string        `json:"time_expression"`
	Entities       []jsonEntity   `json:"entities"`
	Labels         []domain.Label `json:"labels"`
	CausedBy       []int          `json:"caused_by"`
}

// jsonEntity is one scripted mention.
type jsonEntity struct {
	Name string  `json:"name"`
	Kind *string `json:"kind"`
}

// fact returns a conversation-kind world fact with no dimensions, entities or labels.
func fact(what string, entities ...string) jsonFact {
	f := jsonFact{
		What: what, When: "N/A", Where: "N/A", Who: "N/A", Why: "N/A", FactKind: "conversation",
		FactType: "world", Entities: []jsonEntity{}, Labels: []domain.Label{}, CausedBy: []int{},
	}
	for _, n := range entities {
		f.Entities = append(f.Entities, jsonEntity{Name: n})
	}
	return f
}

// factsBody is the EXTRACT response holding facts.
func factsBody(t *testing.T, facts ...jsonFact) string {
	t.Helper()
	if facts == nil {
		facts = []jsonFact{}
	}
	raw, err := json.Marshal(map[string]any{"facts": facts})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// reply is a scripted EXTRACT reply holding facts.
func reply(t *testing.T, facts ...jsonFact) fake.Reply {
	return fake.Reply{JSON: factsBody(t, facts...)}
}

// errExtract is a scripted extraction failure (the "crash").
var errExtract = errors.New("gateway down")

// extractCalls counts concise extraction calls so far.
func (e *env) extractCalls() int {
	return len(e.llm.CallsFor(domain.ModelSlotExtract, prompts.ExtractConcise))
}

// paragraphs returns n paragraphs ("Paragraph i ...") joined by blank lines; with chunkTokens(12) each is
// its own chunk.
func paragraphs(n int, variant string) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = "Paragraph " + string(rune('A'+i)) + " talks about topic " + variant + " in depth."
	}
	return strings.Join(ps, "\n\n")
}

// chunkTokens is an item override setting the chunk budget.
func chunkTokens(n int) *domain.BankConfig {
	return &domain.BankConfig{Retain: &domain.RetainTuning{ChunkMaxTokens: &n}}
}

// ingest runs Ingest and fails the test on error.
func (e *env) ingest(req IngestRequest) IngestResult {
	e.t.Helper()
	if req.Bank.IsZero() {
		req.Bank = testBank
	}
	res, err := e.ingestor.Ingest(e.ctx, req)
	if err != nil {
		e.t.Fatalf("Ingest: %v", err)
	}
	return res
}

// claimRetain claims the next retain_document job.
func (e *env) claimRetain() store.Job {
	e.t.Helper()
	jobs, err := e.store.Claim(e.ctx, "w1", []domain.OperationKind{domain.OperationKindRetainDocument}, 1)
	if err != nil || len(jobs) != 1 {
		e.t.Fatalf("Claim retain_document: %v, %d jobs", err, len(jobs))
	}
	return jobs[0]
}

// document reads a document (with UnitCount filled).
func (e *env) document(id domain.DocumentID) domain.Document {
	e.t.Helper()
	var d domain.Document
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		var err error
		d, err = tx.Documents().Get(e.ctx, testBank, id)
		return err
	})
	if err != nil {
		e.t.Fatalf("Documents.Get %s: %v", id, err)
	}
	return d
}

// operations lists the bank's operations of kind.
func (e *env) operations(kind domain.OperationKind) []domain.Operation {
	e.t.Helper()
	var ops []domain.Operation
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.ctx, testBank, store.OperationFilter{Kind: kind, PageSize: 1000})
		return err
	})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		e.t.Fatalf("Queue.List: %v", err)
	}
	return ops
}

// units reads live units by id.
func (e *env) units(ids []domain.UnitID) []domain.Unit {
	e.t.Helper()
	us, err := e.store.Read().GetUnits(e.ctx, testBank, ids)
	if err != nil {
		e.t.Fatalf("GetUnits: %v", err)
	}
	return us
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
