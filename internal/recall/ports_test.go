package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
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
	e := newEnv(t, envOpts{arms: []Arm{arm(armVector, "oa", "ob", "fa")}})
	e.add(t, fact("fa", "fact a", "a"))
	e.add(t,
		domain.Unit{ID: uid("oa"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs a", Tags: []string{"a"}},
		domain.Unit{ID: uid("ob"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs b", Tags: []string{"b"}})
	stale := func() map[string]bool {
		out := map[string]bool{}
		for _, m := range e.recall(t, Request{Query: "obs"}).Results {
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
