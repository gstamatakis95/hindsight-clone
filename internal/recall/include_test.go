package recall

import (
	"context"
	"memd/internal/domain"
	"testing"
)

// TestIncludeChunksTruncated: chunks are read through Reader.GetChunks in result order, the chunk
// crossing the budget is cut and the walk stops there; nothing is attached when chunks are not asked for.
func TestIncludeChunksTruncated(t *testing.T) {
	env := newEnv(t, envOpts{})
	env.addChunks(t, "d1",
		domain.Chunk{ID: cid("c1"), Index: 0, Text: "one two three"},
		domain.Chunk{ID: cid("c2"), Index: 1, Text: "four five six seven"},
		domain.Chunk{ID: cid("c3"), Index: 2, Text: "eight"},
	)
	unit := func(id, chunk string) candidate {
		u := domain.Unit{ID: uid(id), Type: domain.FactTypeWorld, Text: id}
		if chunk != "" {
			u.ChunkID = cid(chunk)
		}
		return candidate{ID: u.ID, Unit: &u}
	}
	results := []candidate{unit("u1", "c1"), unit("u2", "c2"), unit("u3", "c1"), unit("u4", "c3"), unit("u5", "")}
	inc := domain.RecallInclude{Chunks: ptr(true), ChunksMaxTokens: ptr(5)}
	in := newIncluder(env.store.Read(), wordCounter{})
	e, err := in.Enrich(context.Background(), testBank, domain.SeqRange{}, results, inc)
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Chunks[cid("c1")]; c.Truncated || c.Text != "one two three" {
		t.Fatalf("c1 = %+v", c)
	}
	if c := e.Chunks[cid("c2")]; !c.Truncated || c.Text != "four five" || c.Index != 1 {
		t.Fatalf("c2 = %+v, want cut to two words and truncated", c)
	}
	if _, ok := e.Chunks[cid("c3")]; ok || len(e.Chunks) != 2 {
		t.Fatalf("chunks = %+v, want the walk to stop at the budget", e.Chunks)
	}
	// Not asked: nothing attached.
	e, _ = in.Enrich(context.Background(), testBank, domain.SeqRange{}, results, domain.RecallInclude{})
	if e.Chunks != nil {
		t.Fatalf("chunks not asked for = %+v", e.Chunks)
	}
}

func TestIncludeSourceFactsTruncated(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("f1", "a long fact with many words"), fact("f2", "short fact"), fact("f3", "tiny"))
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs one"})
	e.add(t, domain.Unit{ID: uid("o2"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs two"})
	e.cite(t, "o1", "f1", "f2")
	e.cite(t, "o2", "f3")
	units, err := e.store.Read().GetUnits(context.Background(), testBank, uids("o1", "o2"))
	if err != nil || len(units) != 2 {
		t.Fatalf("observations: %v %v", units, err)
	}
	results := []candidate{{ID: "o1", Unit: &units[0]}, {ID: "o2", Unit: &units[1]}}
	inc := domain.RecallInclude{SourceFacts: ptr(true), SourceFactsMaxTokens: ptr(4)}
	enr, err := newIncluder(e.store.Read(), wordCounter{}).Enrich(context.Background(), testBank,
		domain.SeqRange{}, results, inc)
	if err != nil {
		t.Fatal(err)
	}
	if !enr.SourceFactsTruncated {
		t.Fatal("source facts not reported truncated")
	}
	if _, ok := enr.SourceFacts[uid("f1")]; ok || len(enr.SourceFacts) != 2 {
		t.Fatalf("source facts = %v, want f2 and f3 (f1 skipped)", enr.SourceFacts)
	}
	// Citation order: f1 (o1's first source) is first in line, so with room for exactly 6 words it is kept
	// and the later f2 and f3 skipped.
	inc.SourceFactsMaxTokens = ptr(6)
	enr, _ = newIncluder(e.store.Read(), wordCounter{}).Enrich(context.Background(), testBank,
		domain.SeqRange{}, results, inc)
	if _, ok := enr.SourceFacts[uid("f1")]; !ok || len(enr.SourceFacts) != 1 || !enr.SourceFactsTruncated {
		t.Fatalf("citation order: %v", enr.SourceFacts)
	}
	inc.SourceFactsMaxTokens = nil // default 4096
	enr, _ = newIncluder(e.store.Read(), wordCounter{}).Enrich(context.Background(), testBank,
		domain.SeqRange{}, results, inc)
	if enr.SourceFactsTruncated || len(enr.SourceFacts) != 3 {
		t.Fatalf("default budget: %v truncated=%v", enr.SourceFacts, enr.SourceFactsTruncated)
	}
}

func TestIncludeSourceFactsBoundedBySeq(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.add(t, fact("f1", "early fact"))
	e.add(t, domain.Unit{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs"})
	snapshot, err := e.store.Read().BankSeq(context.Background(), testBank)
	if err != nil {
		t.Fatal(err)
	}
	e.add(t, fact("f2", "late fact")) // after the snapshot
	e.cite(t, "o1", "f1", "f2")
	units, _ := e.store.Read().GetUnits(context.Background(), testBank, uids("o1"))
	results := []candidate{{ID: "o1", Unit: &units[0]}}
	inc := domain.RecallInclude{SourceFacts: ptr(true)}
	enr, err := newIncluder(e.store.Read(), wordCounter{}).Enrich(context.Background(), testBank,
		domain.SeqUpTo(0, snapshot), results, inc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := enr.SourceFacts[uid("f1")]; !ok || len(enr.SourceFacts) != 1 || enr.SourceFactsTruncated {
		t.Fatalf("source facts = %v, want only f1 inside the snapshot", enr.SourceFacts)
	}
}
