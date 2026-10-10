package recall

import (
	"context"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/routing"
	"memd/internal/engine/timex"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
	"time"
)

// testBank is the bank every test recalls from.
var testBank = port.BankRef{Tenant: "t1", Bank: "b1"}

// testNow is the frozen clock time of every test.
var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// wordCounter counts whitespace-separated words: a deterministic TokenCounter for tests.
type wordCounter struct{}

func (wordCounter) Count(s string) int { return len(strings.Fields(s)) }

// stubLookups serves fixed tags, chunks and entity states.
type stubLookups struct {
	tags     []string
	chunks   map[port.ChunkID]domain.Chunk
	entities []domain.EntityState
}

func (s *stubLookups) BankTags(context.Context, port.BankRef) ([]string, error) { return s.tags, nil }

func (s *stubLookups) GetChunks(_ context.Context, _ port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error) {
	var out []domain.Chunk
	for _, id := range ids {
		if c, ok := s.chunks[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *stubLookups) EntityStates(context.Context, port.BankRef, []port.UnitID) ([]domain.EntityState, error) {
	return s.entities, nil
}

// stubArm is a Retriever returning fixed hits and recording the query it saw.
type stubArm struct {
	name   string
	hits   []port.Hit
	seen   *ParsedQuery
	called int
}

func (a *stubArm) Name() string { return a.name }

func (a *stubArm) Retrieve(_ context.Context, q *ParsedQuery) ([]Candidate, error) {
	a.seen, a.called = q, a.called+1
	return candidates(a.name, a.hits, q.Limit), nil
}

// arm builds a stubArm whose hits are ids in order with descending scores.
func arm(name string, ids ...port.UnitID) *stubArm {
	a := &stubArm{name: name}
	for i, id := range ids {
		a.hits = append(a.hits, port.Hit{ID: id, Score: 1 - float64(i)/100})
	}
	return a
}

// env is a recall test environment over the fakes.
type env struct {
	store   *fake.Store
	clock   *fake.Clock
	emb     *fake.Embedder
	rr      *fake.Reranker
	llm     *fake.LLM
	lookups *stubLookups
	rc      Recaller
}

// envOpts tweaks newEnv.
type envOpts struct {
	rerank bool        // bind RERANK (recall.rerank stays on)
	arms   []Retriever // stub arms instead of the real four
}

// newEnv builds a store with testBank, the fakes and a Recaller wired like production.
func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	e := &env{
		clock: fake.NewClock(testNow), emb: fake.NewEmbedder(fake.EmbedSimilar), rr: fake.NewReranker(),
		llm: fake.NewLLM(), lookups: &stubLookups{},
	}
	e.store = fake.NewStore(e.clock)
	vars := map[string]string{config.EnvModel: "llm-1"}
	if o.rerank {
		vars[config.EnvModelPrefix+"RERANK"] = "rerank-1"
	}
	server, err := config.ServerDefaults(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	router := routing.NewRouter()
	e.rc = NewRecaller(RecallerDeps{
		Reader: e.store.Read(), Lookups: e.lookups, Config: config.NewResolver(e.store, nil, server),
		Embedder: e.emb, Reranker: e.rr, Router: router, Times: timex.NewNormalizer(e.llm, router, e.clock),
		Clock: e.clock, Count: wordCounter{}, Retrievers: o.arms,
	})
	e.tx(t, func(ctx context.Context, tx port.Tx, _ int64) error {
		_, err := tx.Banks().Ensure(ctx, testBank)
		return err
	})
	return e
}

// tx runs fn in a transaction that took the bank's seq.
func (e *env) tx(t *testing.T, fn func(ctx context.Context, tx port.Tx, seq int64) error) {
	t.Helper()
	ctx := context.Background()
	err := e.store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, testBank); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		return fn(ctx, tx, seq)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fact builds a world fact with normalized tags.
func fact(id, text string, tags ...string) domain.Unit {
	return domain.Unit{
		ID: port.UnitID(id), Bank: testBank, Type: domain.FactTypeWorld, Text: text, Tags: domain.NormalizeTags(tags),
	}
}

// add inserts units (facts and observations) with embeddings of their text.
func (e *env) add(t *testing.T, us ...domain.Unit) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx port.Tx, seq int64) error {
		for _, u := range us {
			u.Seq, u.Embedding = seq, e.emb.Vector(u.Text)
			if u.Tags == nil {
				u.Tags = []string{}
			}
			var err error
			if u.Type == domain.FactTypeObservation {
				err = tx.Units().UpsertObservation(ctx, u)
			} else {
				err = tx.Units().Insert(ctx, []domain.Unit{u})
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// cite records that observation obs cites the facts srcs.
func (e *env) cite(t *testing.T, obs string, srcs ...string) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx port.Tx, _ int64) error {
		ev := make([]domain.Evidence, len(srcs))
		for i, s := range srcs {
			ev[i] = domain.Evidence{Unit: port.UnitID(s), Quote: s}
		}
		return tx.Evidence().Add(ctx, port.UnitID(obs), ev)
	})
}

// link adds a semantic link from -> to.
func (e *env) link(t *testing.T, from, to string, w float64) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx port.Tx, _ int64) error {
		return tx.Links().Insert(ctx, []domain.Link{{
			Bank: testBank, From: port.UnitID(from), To: port.UnitID(to), Kind: domain.LinkSemantic, Weight: w,
		}})
	})
}

// recall runs a recall and fails the test on error.
func (e *env) recall(t *testing.T, req RecallRequest) RecallResult {
	t.Helper()
	if req.Bank == (port.BankRef{}) {
		req.Bank = testBank
	}
	res, err := e.rc.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return res
}

// ids returns the result ids in order.
func ids(r RecallResult) []string {
	out := make([]string, len(r.Results))
	for i, m := range r.Results {
		out[i] = string(m.Unit.ID)
	}
	return out
}

// sortedIDs returns the result ids sorted.
func sortedIDs(r RecallResult) []string {
	out := ids(r)
	slices.Sort(out)
	return out
}

// recallCfg wraps a RecallConfig in request overrides.
func recallCfg(rc domain.RecallConfig) *domain.BankConfig { return &domain.BankConfig{Recall: &rc} }

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// at returns testNow shifted by d days.
func at(days int) *time.Time {
	v := testNow.AddDate(0, 0, days)
	return &v
}
