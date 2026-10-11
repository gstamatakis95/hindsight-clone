package arms_test

import (
	"context"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/graph"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"memd/internal/timex"
	"slices"
	"sync"
	"testing"
	"time"
)

// testBank is the bank every test recalls from.
var testBank = domain.BankRef{Tenant: "t1", Bank: "b1"}

// testNow is the frozen clock time of every test.
var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// uid is the unit id of a readable fixture name.
func uid(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

// env is the recall engine with the four built-in arms, as cmd/memd/wire_services.go registers them, over a store on
// its own database and the model fakes.
type env struct {
	store *postgres.DB
	clock *clock.Manual
	emb   *fake.Embedder
	llm   *fake.LLM
	deps  recall.Deps
	rc    *recall.Engine
}

// newEnv builds the env with testBank.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{clock: clock.NewManual(testNow), emb: fake.NewEmbedder(fake.EmbedSimilar), llm: fake.NewLLM()}
	e.store = pgtest.NewStore(t)
	server, err := config.ServerDefaults(domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "llm-1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ai.NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	router := ai.NewRouter()
	e.deps = recall.Deps{
		DB: e.store, Config: config.NewResolver(e.store, server), Embedder: e.emb, Router: router,
		Times: timex.New(e.llm, router, e.clock), Clock: e.clock, Tokens: tok,
	}
	e.rc = e.engine(e.store.Read())
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		_, err := tx.Banks().Ensure(ctx, testBank)
		return err
	})
	return e
}

// engine builds an Engine over e's dependencies whose arms read s.
func (e *env) engine(s store.Search) *recall.Engine {
	d := e.deps
	d.Arms = builtins(s, e.store.Graph())
	return recall.New(d)
}

// builtins are the built-in arms in their registration order.
func builtins(s store.Search, g graph.Reader) []recall.Arm {
	return []recall.Arm{arms.NewVector(s), arms.NewText(s), arms.NewGraph(s, g), arms.NewTemporal(s)}
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

// add inserts facts with embeddings of their text.
func (e *env) add(t *testing.T, us ...domain.Unit) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx store.Tx, seq int64) error {
		for i := range us {
			us[i].Seq, us[i].Embedding = seq, e.emb.Vector(us[i].Text)
			if us[i].Tags == nil {
				us[i].Tags = []string{}
			}
		}
		return tx.Units().Insert(ctx, us)
	})
}

// link adds a link from -> to of kind.
func (e *env) link(t *testing.T, from, to string, kind graph.Kind, w float64) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		return tx.Graph().AddLinks(ctx, []graph.Link{{Bank: testBank, From: uid(from), To: uid(to), Kind: kind, Weight: w}})
	})
}

// recall runs a recall on testBank and fails the test on error.
func (e *env) recall(t *testing.T, req recall.Request) recall.Result {
	t.Helper()
	req.Bank = testBank
	res, err := e.rc.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return res
}

// ids returns the results' fixture names in order.
func ids(r recall.Result) []string {
	out := make([]string, len(r.Results))
	for i, m := range r.Results {
		out[i] = pgtest.Name(m.Unit.ID)
	}
	return out
}

// sortedIDs returns the result ids sorted.
func sortedIDs(r recall.Result) []string {
	out := ids(r)
	slices.Sort(out)
	return out
}

// armNames returns the arms of a recall's trace in order.
func armNames(r recall.Result) []string {
	var out []string
	for _, a := range r.Trace.Arms {
		out = append(out, a.Arm)
	}
	return out
}

// spy wraps a store.Search and records the vector, text and temporal queries it serves.
type spy struct {
	store.Search
	mu        sync.Mutex
	vectors   []store.VectorQuery
	texts     []store.TextQuery
	temporals []store.TemporalQuery
}

func (s *spy) SearchVector(ctx context.Context, q store.VectorQuery) ([]store.Hit, error) {
	s.mu.Lock()
	s.vectors = append(s.vectors, q)
	s.mu.Unlock()
	return s.Search.SearchVector(ctx, q)
}

func (s *spy) SearchText(ctx context.Context, q store.TextQuery) ([]store.Hit, error) {
	s.mu.Lock()
	s.texts = append(s.texts, q)
	s.mu.Unlock()
	return s.Search.SearchText(ctx, q)
}

func (s *spy) SearchTemporal(ctx context.Context, q store.TemporalQuery) ([]store.Hit, error) {
	s.mu.Lock()
	s.temporals = append(s.temporals, q)
	s.mu.Unlock()
	return s.Search.SearchTemporal(ctx, q)
}

// leaf builds a leaf tag group.
func leaf(m domain.TagsMatch, tags ...string) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: tags, Match: m}}
}

// group builds an all_of / any_of group, or a none_of over gs[0].
func group(kind domain.TagGroupKind, gs ...domain.TagGroup) domain.TagGroup {
	if kind == domain.TagGroupNoneOf {
		return domain.TagGroup{Kind: kind, Not: &gs[0]}
	}
	return domain.TagGroup{Kind: kind, Groups: gs}
}

// at returns testNow shifted by d days.
func at(days int) *time.Time {
	v := testNow.AddDate(0, 0, days)
	return &v
}
