package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"sync"
	"testing"
	"time"
)

// addChunks stores document doc with the given chunks (ids, indexes and texts) in the store.
func (e *env) addChunks(t *testing.T, doc domain.DocumentID, cs ...domain.Chunk) {
	t.Helper()
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		if err := tx.Documents().Upsert(ctx, domain.Document{ID: doc, Bank: testBank}); err != nil {
			return err
		}
		for i := range cs {
			cs[i].Bank, cs[i].DocumentID = testBank, doc
		}
		return tx.Chunks().Insert(ctx, cs)
	})
}

// TestRecallFuzzyTagsThroughTagCounts: a fuzzy leaf resolves against the bank's tags read with
// Reader.TagCounts, so a typo matches the real tag and the filter still excludes other tags.
func TestRecallFuzzyTagsThroughTagCounts(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("eng", "rocket launch notes", "engineering"), fact("mkt", "rocket launch notes", "marketing"))
	g := gFuzzy(domain.TagsMatchAnyStrict, "enginering")
	res := e.recall(t, RecallRequest{Query: "rocket launch notes", Tags: domain.TagFilter{Groups: []domain.TagGroup{g}}})
	if got := ids(res); !slices.Equal(got, []string{"eng"}) {
		t.Fatalf("fuzzy 'enginering' = %v, want [eng]", got)
	}
}

// TestBankTagsMostUsedFirst: the vocabulary read through Reader.TagCounts is ordered by count desc, then
// tag, not TagCounts' byte order.
func TestBankTagsMostUsedFirst(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("1", "x", "b", "z"), fact("2", "x", "z"), fact("3", "x", "a"), fact("4", "x", "z", "a"))
	got, err := bankTags(context.Background(), e.store.Read(), testBank)
	if err != nil || !slices.Equal(got, []string{"z", "a", "b"}) {
		t.Fatalf("bankTags = %v, %v; want [z a b]", got, err)
	}
}

// TestRecallMemoryStale: Memory.Stale is set from Reader.StaleScopes on observations whose scope
// holds an unconsolidated fact, never on facts, and clears once the fact is consolidated.
func TestRecallMemoryStale(t *testing.T) {
	e := newEnv(t, envOpts{arms: []Retriever{arm(ArmSemantic, "oa", "ob", "fa")}})
	e.add(t, fact("fa", "fact a", "a"))
	e.add(t,
		domain.Unit{ID: uid("oa"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs a", Tags: []string{"a"}},
		domain.Unit{ID: uid("ob"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs b", Tags: []string{"b"}})
	stale := func() map[string]bool {
		out := map[string]bool{}
		for _, m := range e.recall(t, RecallRequest{Query: "obs"}).Results {
			out[pgtest.Name(m.Unit.ID)] = m.Stale
		}
		return out
	}
	if got := stale(); !got["oa"] || got["ob"] || got["fa"] || len(got) != 3 {
		t.Fatalf("stale = %v, want only oa", got)
	}
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		return tx.Units().MarkConsolidated(ctx, uids("fa"), testNow)
	})
	if got := stale(); got["oa"] {
		t.Fatalf("stale after consolidation = %v, want none", got)
	}
}

// temporalSpy wraps the store's reader and records the temporal queries it serves.
type temporalSpy struct {
	store.Reader
	mu sync.Mutex
	qs []store.TemporalQuery
}

// SearchTemporal records q and delegates.
func (s *temporalSpy) SearchTemporal(ctx context.Context, q store.TemporalQuery) ([]store.Hit, error) {
	s.mu.Lock()
	s.qs = append(s.qs, q)
	s.mu.Unlock()
	return s.Reader.SearchTemporal(ctx, q)
}

// TestTemporalArmRelevanceFilter: the temporal arm passes the query embedding and the 0.1 floor, so
// SearchTemporal drops an in-window unit with no embedding (the filter is on) and keeps the relevant one.
func TestTemporalArmRelevanceFilter(t *testing.T) {
	e := newEnv(t, envOpts{})
	at := testNow.Add(-24 * time.Hour)
	rel, noEmb := fact("rel", "rocket launch"), fact("noemb", "rocket launch")
	rel.OccurredStart, noEmb.OccurredStart = &at, &at
	e.add(t, rel)
	e.tx(t, func(ctx context.Context, tx store.Tx, seq int64) error {
		noEmb.Seq, noEmb.Tags = seq, []string{}
		return tx.Units().Insert(ctx, []domain.Unit{noEmb})
	})
	spy := &temporalSpy{Reader: e.store.Read()}
	w := &domain.TimeWindow{Start: testNow.AddDate(0, 0, -7), End: testNow}
	q := &parsedQuery{Bank: testBank, Window: w, Embedding: e.emb.Vector("rocket launch"), Limit: 10}
	cs, err := newTemporal(spy).Retrieve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].ID != uid("rel") {
		t.Fatalf("temporal arm = %+v, want only rel", cs)
	}
	if len(spy.qs) != 1 || spy.qs[0].MinSimilarity != 0.1 || len(spy.qs[0].Embedding) == 0 {
		t.Fatalf("temporal query = %+v, want the query embedding and MinSimilarity 0.1", spy.qs)
	}
}
