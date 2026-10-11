package recall

import (
	"context"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/timex"
	"memd/internal/fake"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"strings"
	"testing"
	"time"
)

// testBank is the bank every test recalls from.
var testBank = domain.BankRef{Tenant: "t1", Bank: "b1"}

// testNow is the frozen clock time of every test.
var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// uid is the unit id of a readable fixture name (Postgres stores unit ids as uuid).
func uid(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

// uids maps uid over names.
func uids(names ...string) []domain.UnitID { return pgtest.IDs[domain.UnitID](names...) }

// cid is the chunk id of a readable fixture name.
func cid(name string) domain.ChunkID { return pgtest.ID[domain.ChunkID](name) }

// wordCounter counts whitespace-separated words: a deterministic TokenCounter for tests.
type wordCounter struct{}

func (wordCounter) Count(s string) int { return len(strings.Fields(s)) }

// stubArm is a Retriever returning fixed hits and recording the query it saw.
type stubArm struct {
	name   string
	hits   []store.Hit
	seen   *parsedQuery
	called int
}

func (a *stubArm) Name() string { return a.name }

func (a *stubArm) Retrieve(_ context.Context, q *parsedQuery) ([]Candidate, error) {
	a.seen, a.called = q, a.called+1
	return candidates(a.name, a.hits, q.Limit), nil
}

// arm builds a stubArm whose hits are the named units in order with descending scores.
func arm(name string, names ...domain.UnitID) *stubArm {
	a := &stubArm{name: name}
	for i, n := range names {
		a.hits = append(a.hits, store.Hit{ID: uid(string(n)), Score: 1 - float64(i)/100})
	}
	return a
}

// env is a recall test environment over a store on its own database and the model fakes.
type env struct {
	store *postgres.DB
	clock *clock.Manual
	emb   *fake.Embedder
	rr    *fake.Reranker
	llm   *fake.LLM
	rc    Recaller
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
		clock: clock.NewManual(testNow), emb: fake.NewEmbedder(fake.EmbedSimilar), rr: fake.NewReranker(),
		llm: fake.NewLLM(),
	}
	e.store = pgtest.NewStore(t)
	models := &domain.ModelBindings{Default: &domain.ModelBinding{Model: "llm-1"}}
	if o.rerank {
		models.Rerank = &domain.ModelBinding{Model: "rerank-1"}
	}
	server, err := config.ServerDefaults(domain.BankConfig{Models: models})
	if err != nil {
		t.Fatal(err)
	}
	router := ai.NewRouter()
	e.rc = NewRecaller(RecallerDeps{
		Reader: e.store.Read(), Config: config.NewResolver(e.store, nil, server),
		Embedder: e.emb, Reranker: e.rr, Router: router, Times: timex.NewNormalizer(e.llm, router, e.clock),
		Clock: e.clock, Count: wordCounter{}, Retrievers: o.arms,
	})
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		_, err := tx.Banks().Ensure(ctx, testBank)
		return err
	})
	return e
}

// tx runs fn in a transaction that took the bank's seq.
func (e *env) tx(t *testing.T, fn func(ctx context.Context, tx store.Tx, seq int64) error) {
	t.Helper()
	ctx := context.Background()
	err := e.store.InTx(ctx, func(tx store.Tx) error {
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
		ID: uid(id), Bank: testBank, Type: domain.FactTypeWorld, Text: text, Tags: domain.NormalizeTags(tags),
	}
}

// add inserts units (facts and observations) with embeddings of their text.
func (e *env) add(t *testing.T, us ...domain.Unit) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx store.Tx, seq int64) error {
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
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		ev := make([]domain.Evidence, len(srcs))
		for i, s := range srcs {
			ev[i] = domain.Evidence{Unit: uid(s), Quote: s}
		}
		return tx.Evidence().Add(ctx, uid(obs), ev)
	})
}

// link adds a semantic link from -> to.
func (e *env) link(t *testing.T, from, to string, w float64) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		return tx.Links().Insert(ctx, []domain.Link{{
			Bank: testBank, From: uid(from), To: uid(to), Kind: domain.LinkSemantic, Weight: w,
		}})
	})
}

// recall runs a recall and fails the test on error.
func (e *env) recall(t *testing.T, req RecallRequest) RecallResult {
	t.Helper()
	if req.Bank == (domain.BankRef{}) {
		req.Bank = testBank
	}
	res, err := e.rc.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return res
}

// ids returns the results' fixture names in order.
func ids(r RecallResult) []string {
	out := make([]string, len(r.Results))
	for i, m := range r.Results {
		out[i] = pgtest.Name(m.Unit.ID)
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
