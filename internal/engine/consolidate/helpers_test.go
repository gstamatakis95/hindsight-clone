package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/routing"
	"memd/internal/fake"
	"memd/internal/jsonschema"
	"memd/internal/port"
	"sync"
	"testing"
	"time"
)

// bank is the test bank.
var bank = port.BankRef{Tenant: "t1", Bank: "b1"}

// errPoison is what the test LLM returns for a batch holding a poison unit.
var errPoison = errors.New("poison input")

// funcLLM is a port.LLM whose Structured replies are computed per call, then validated against the
// request's schema exactly like the gateway and the fake (internal/jsonschema), so a reply citing a fact
// outside the batch or exceeding maxItems fails the call.
type funcLLM struct {
	mu          sync.Mutex
	consolidate func(req port.StructuredRequest, ids []string) (string, error)
	dedup       func(req port.StructuredRequest) (string, error)
	calls       []port.StructuredRequest
}

var _ port.LLM = (*funcLLM)(nil)

// Structured serves one call from the slot's handler.
func (l *funcLLM) Structured(ctx context.Context, req port.StructuredRequest, out any) (port.Usage, error) {
	sch, err := jsonschema.Compile(req.Schema)
	if err != nil {
		return port.Usage{}, fmt.Errorf("test llm: %w", err)
	}
	l.mu.Lock()
	l.calls = append(l.calls, req)
	consolidate, dedup := l.consolidate, l.dedup
	l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return port.Usage{}, fmt.Errorf("test llm: %w", err)
	}
	var body string
	switch {
	case req.Slot == domain.ModelSlotConsolidate && consolidate != nil:
		body, err = consolidate(req, schemaFactIDs(req.Schema))
	case req.Slot == domain.ModelSlotDedupCheck && dedup != nil:
		body, err = dedup(req)
	default:
		err = fmt.Errorf("no handler for slot %s", req.Slot)
	}
	if err != nil {
		return port.Usage{}, fmt.Errorf("%w: test llm: %w", domain.ErrUnavailable, err)
	}
	if err := sch.Validate([]byte(body)); err != nil {
		return port.Usage{}, fmt.Errorf("%w: test llm: invalid output: %w", domain.ErrUnavailable, err)
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return port.Usage{}, fmt.Errorf("%w: test llm: %w", domain.ErrUnavailable, err)
	}
	return port.Usage{Slot: req.Slot, Model: req.Binding.Model, InputTokens: 10, OutputTokens: 2}, nil
}

// Chat is unused by consolidation.
func (l *funcLLM) Chat(context.Context, port.ChatRequest) (port.ChatResponse, error) {
	return port.ChatResponse{}, errors.New("test llm: no chat")
}

// callsFor returns the recorded requests of slot.
func (l *funcLLM) callsFor(slot port.ModelSlot) []port.StructuredRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []port.StructuredRequest
	for _, c := range l.calls {
		if c.Slot == slot {
			out = append(out, c)
		}
	}
	return out
}

// schemaFactIDs reads the per-call source_fact_ids enum (the batch's fact ids) from a CONSOLIDATE schema.
func schemaFactIDs(raw json.RawMessage) []string {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	items, ok := dig(root, "properties", "creates", "items", "properties", "source_fact_ids", "items")
	if !ok {
		return nil
	}
	enum, _ := items["enum"].([]any)
	out := make([]string, 0, len(enum))
	for _, v := range enum {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// create is one create of a scripted CONSOLIDATE reply.
type create struct {
	Text          string   `json:"text"`
	SourceFactIDs []string `json:"source_fact_ids"`
	Reason        string   `json:"reason"`
}

// update is one update of a scripted CONSOLIDATE reply.
type update struct {
	ObservationID string   `json:"observation_id"`
	Text          string   `json:"text"`
	SourceFactIDs []string `json:"source_fact_ids"`
	Reason        string   `json:"reason"`
}

// reply renders a CONSOLIDATE reply.
func reply(creates []create, updates []update) string {
	if creates == nil {
		creates = []create{}
	}
	if updates == nil {
		updates = []update{}
	}
	b, _ := json.Marshal(map[string]any{"creates": creates, "updates": updates})
	return string(b)
}

// env is a consolidation test environment over the fakes.
type env struct {
	t      *testing.T
	clock  *fake.Clock
	store  *fake.Store
	llm    *funcLLM
	emb    *fake.Embedder
	deps   ConsolidatorDeps
	texts  map[port.UnitID]string
	nextID int
}

// newEnv creates the bank with cfg as its own config and wires a Consolidator with the real config
// resolver and StrategyMatcher. The default CONSOLIDATE handler creates one observation per fact.
func newEnv(t *testing.T, cfg domain.BankConfig) *env {
	t.Helper()
	clock := fake.NewClock(time.Time{})
	e := &env{
		t: t, clock: clock, store: fake.NewStore(clock), llm: &funcLLM{},
		emb: fake.NewEmbedder(fake.EmbedSimilar), texts: map[port.UnitID]string{},
	}
	e.llm.consolidate = e.createEach
	server, err := config.ServerDefaults(func(k string) string {
		if k == config.EnvModel {
			return "base-model"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	e.tx(func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), bank); err != nil {
			return err
		}
		return tx.Banks().SetConfig(t.Context(), bank, cfg)
	})
	e.deps = ConsolidatorDeps{
		Store:    e.store,
		Config:   config.NewResolver(e.store, NewStrategyMatcher(nil), server),
		Router:   routing.NewRouter(),
		LLM:      e.llm,
		Embedder: e.emb,
		Clock:    clock,
		Counter:  storeCounter{e.store},
		NewID: func() port.UnitID {
			e.nextID++
			return port.UnitID(fmt.Sprintf("obs-%02d", e.nextID))
		},
	}
	return e
}

// consolidator builds the Consolidator from e.deps.
func (e *env) consolidator() Consolidator { return NewConsolidator(e.deps) }

// tx runs fn in a write transaction and fails the test on error.
func (e *env) tx(fn func(tx port.Tx) error) {
	e.t.Helper()
	if err := e.store.InTx(e.t.Context(), fn); err != nil {
		e.t.Fatal(err)
	}
}

// scopesTx runs fn in a write transaction and returns its scopes, failing the test on error.
func (e *env) scopesTx(fn func(tx port.Tx) ([]port.ScopeKey, error)) []port.ScopeKey {
	e.t.Helper()
	var out []port.ScopeKey
	e.tx(func(tx port.Tx) error {
		var err error
		if out, err = fn(tx); err != nil {
			return fmt.Errorf("in tx: %w", err)
		}
		return nil
	})
	return out
}

// createEach answers CONSOLIDATE with one create per fact of the batch.
func (e *env) createEach(_ port.StructuredRequest, ids []string) (string, error) {
	var cs []create
	for _, id := range ids {
		cs = append(cs, create{Text: "Observed: " + e.texts[port.UnitID(id)], SourceFactIDs: []string{id}})
	}
	return reply(cs, nil), nil
}

// fact inserts a world fact with tags (normalized) and an embedding of its text.
func (e *env) fact(id, text string, tags ...string) {
	e.t.Helper()
	e.texts[port.UnitID(id)] = text
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(e.t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(e.t.Context(), []domain.Unit{{
			ID: port.UnitID(id), Bank: bank, Type: domain.FactTypeWorld, Text: text,
			Tags: domain.NormalizeTags(tags), Embedding: e.emb.Vector(text), Seq: seq,
		}})
	})
}

// observation inserts an observation of scope tags citing sources (which it stamps consolidated).
func (e *env) observation(id, text string, tags []string, sources ...string) {
	e.t.Helper()
	e.tx(func(tx port.Tx) error {
		ctx := e.t.Context()
		seq, err := tx.Seq(ctx, bank)
		if err != nil {
			return err
		}
		err = tx.Units().UpsertObservation(ctx, domain.Unit{
			ID: port.UnitID(id), Bank: bank, Type: domain.FactTypeObservation, Text: text,
			Tags: domain.NormalizeTags(tags), Embedding: e.emb.Vector(text), Seq: seq, Version: 1,
			ProofCount: len(sources),
		})
		if err != nil {
			return err
		}
		var ev []domain.Evidence
		var ids []port.UnitID
		for _, s := range sources {
			ev = append(ev, domain.Evidence{Unit: port.UnitID(s), Quote: e.texts[port.UnitID(s)]})
			ids = append(ids, port.UnitID(s))
		}
		if err := tx.Evidence().Add(ctx, port.UnitID(id), ev); err != nil {
			return err
		}
		return tx.Units().MarkConsolidated(ctx, ids, e.clock.Now())
	})
}

// unit reads one live unit.
func (e *env) unit(id string) domain.Unit {
	e.t.Helper()
	us, err := e.store.Read().GetUnits(e.t.Context(), bank, []port.UnitID{port.UnitID(id)})
	if err != nil || len(us) != 1 {
		e.t.Fatalf("unit %s: %v (%d found)", id, err, len(us))
	}
	return us[0]
}

// observations returns every live observation of the bank, by id.
func (e *env) observations() []domain.Unit {
	e.t.Helper()
	return listObservations(e.t.Context(), e.t, e.store.Read(), nil)
}

// listObservations lists the bank's observations (only those of scope s when s is non-nil) with a
// zero-vector semantic search, which the fake answers with every unit that has an embedding.
func listObservations(ctx context.Context, t *testing.T, r port.Reader, s *port.ScopeKey) []domain.Unit {
	q := port.VectorQuery{
		Bank: bank, Embedding: make([]float32, domain.EmbeddingDim),
		Types: []domain.FactType{domain.FactTypeObservation},
	}
	if s != nil {
		q.Tags = scopePredicate(*s)
	}
	hits, err := r.SearchVector(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]port.UnitID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	us, err := r.GetUnits(ctx, bank, ids)
	if err != nil {
		t.Fatal(err)
	}
	return us
}

// jobs lists the bank's pending operations of kind.
func (e *env) jobs(kind domain.OperationKind) []domain.Operation {
	e.t.Helper()
	var ops []domain.Operation
	e.tx(func(tx port.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.t.Context(), bank, port.OperationFilter{
			Status: domain.OperationStatusPending, Kind: kind, PageSize: 100,
		})
		return err
	})
	return ops
}

// wantPending fails the test unless the bank has n pending operations of kind.
func (e *env) wantPending(kind domain.OperationKind, n int) {
	e.t.Helper()
	if got := len(e.jobs(kind)); got != n {
		e.t.Fatalf("pending %s operations = %d, want %d", kind, got, n)
	}
}

// run runs j and fails the test on error.
func (e *env) run(j port.Job) ConsolidationResult {
	e.t.Helper()
	res, err := e.consolidator().Run(e.t.Context(), j)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// storeCounter is the test ObservationCounter: it counts a scope's observations through the fake's search.
type storeCounter struct{ s port.Store }

// CountObservations counts the observations of scope s.
func (c storeCounter) CountObservations(ctx context.Context, s port.ScopeKey) (int, error) {
	q := port.VectorQuery{
		Bank: s.BankRef, Embedding: make([]float32, domain.EmbeddingDim),
		Types: []domain.FactType{domain.FactTypeObservation}, Tags: scopePredicate(s),
	}
	hits, err := c.s.Read().SearchVector(ctx, q)
	return len(hits), err
}

// autoJob is the automatic consolidate job of the test bank.
func autoJob() port.Job {
	j := NewJob(bank, nil, nil)
	j.ID = "op-1"
	return j
}

// scopeOf builds a scope key of the test bank.
func scopeOf(tags ...string) port.ScopeKey { return domain.NewScopeKey(bank, tags) }

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// hasFact reports whether ids contain id.
func hasFact(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// consolidateModel binds the CONSOLIDATE slot to model.
func consolidateModel(model string) []domain.SlotBinding {
	return []domain.SlotBinding{{Slot: domain.ModelSlotConsolidate, Binding: domain.ModelBinding{Model: model}}}
}
