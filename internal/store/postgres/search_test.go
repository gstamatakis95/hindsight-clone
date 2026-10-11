package postgres_test

import (
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"testing"
)

// ---------- tag and seq filters on all four recall arms ----------

// armTagSets are the tags of the arm fixture's units: they make every predicate of tagPredicates select a
// different subset (see TestTagPredicatesDiscriminate).
var armTagSets = [][]string{{}, {"alpha"}, {"beta"}, {"alpha", "beta"}, {"gamma"}}

// armFixture holds one unit per armTagSets entry, each inserted in its own transaction (so seqs strictly
// increase) and each a hit of every arm with no filter: its embedding is close to angleVec(0), its text
// contains "zebra", it occurs inside armWindow and the seed links to it. The seed itself matches no arm.
type armFixture struct {
	seed  domain.Unit
	units []domain.Unit
}

// armWindow is the temporal arm's query window; every fixture unit occurs inside it.
var armWindow = domain.TimeWindow{Start: day(60), End: day(90)}

// newArmFixture inserts the seed, then the units, then the seed's links to every unit.
func newArmFixture(t *testing.T, s store.DB) armFixture {
	t.Helper()
	ensureBanks(t, s, bankA)
	f := armFixture{seed: fact(bankA, "graph seed")}
	f.seed.Timeless = true
	insertUnits(t, s, f.seed)
	links := make([]graph.Link, len(armTagSets))
	for i, tags := range armTagSets {
		u := dated(bankA, fmt.Sprintf("zebra sighting number %d", i), day(70+i), day(71+i))
		u.Tags = domain.NormalizeTags(tags)
		u.Embedding = angleVec(float64(10 * i))
		u.Seq = insertUnits(t, s, u)
		f.units = append(f.units, u)
		links[i] = link(bankA, f.seed.ID, u.ID, graph.KindSemantic, 0.9-0.1*float64(i))
	}
	insertLinks(t, s, links...)
	return f
}

// want returns the fixture units, in insertion order, that tags and seq both admit.
func (f armFixture) want(tags *domain.TagPredicate, seq domain.SeqRange) []domain.UnitID {
	var out []domain.UnitID
	for _, u := range f.units {
		if tags.Eval(u.Tags) && seq.Contains(u.Seq) {
			out = append(out, u.ID)
		}
	}
	return out
}

// recallArm is one Reader search under test with a tag filter and a seq range.
type recallArm struct {
	name string
	// ordered arms have a known hit order (the vector arm: fixture order is nearest first).
	ordered bool
	search  func(t *testing.T, s store.DB, f armFixture, tags *domain.TagPredicate, seq domain.SeqRange) []store.Hit
}

// recallArms are the four Reader arms, each querying the arm fixture.
func recallArms() []recallArm {
	return []recallArm{
		{"SearchVector", true, func(t *testing.T, s store.DB, _ armFixture, p *domain.TagPredicate, r domain.SeqRange,
		) []store.Hit {
			return searchVector(t, s, store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Tags: p, Seq: r, Limit: 10})
		}},
		{"SearchText", false, func(t *testing.T, s store.DB, _ armFixture, p *domain.TagPredicate, r domain.SeqRange,
		) []store.Hit {
			return searchText(t, s, store.TextQuery{Bank: bankA, Text: "zebra", Tags: p, Seq: r, Limit: 10})
		}},
		{"SearchTemporal", false, func(t *testing.T, s store.DB, _ armFixture, p *domain.TagPredicate, r domain.SeqRange,
		) []store.Hit {
			return searchTemporal(t, s, store.TemporalQuery{Bank: bankA, Window: armWindow, Tags: p, Seq: r, Limit: 10})
		}},
		{"ExpandGraph", false, func(t *testing.T, s store.DB, f armFixture, p *domain.TagPredicate, r domain.SeqRange,
		) []store.Hit {
			q := graph.Query{Bank: bankA, Seeds: []domain.UnitID{f.seed.ID}, MaxHops: 1, Tags: p, Seq: r, Limit: 10}
			return expandGraph(t, s, q)
		}},
	}
}

// armCases are two store subtests per recall arm: every tag predicate, and every kind of seq range.
func armCases() []storeCase {
	var cases []storeCase
	for _, a := range recallArms() {
		cases = append(cases,
			storeCase{a.name + "TagPredicates", func(t *testing.T, s store.DB) { testArmTags(t, s, a) }},
			storeCase{a.name + "SeqRange", func(t *testing.T, s store.DB) { testArmSeq(t, s, a) }},
		)
	}
	return cases
}

// wantArmHits compares an arm's hits with want, in order for ordered arms and as a set otherwise.
func wantArmHits(t *testing.T, a recallArm, what string, hs []store.Hit, want []domain.UnitID) {
	t.Helper()
	if a.ordered {
		wantSeq(t, what, hitIDs(hs), want)
		return
	}
	wantSet(t, what, hitIDs(hs), want)
}

// testArmTags: every tag predicate selects exactly the units (*domain.TagPredicate).Eval accepts.
func testArmTags(t *testing.T, s store.DB, a recallArm) {
	f := newArmFixture(t, s)
	for _, c := range tagPredicates() {
		hs := a.search(t, s, f, c.p, domain.SeqRange{})
		wantArmHits(t, a, a.name+" Tags="+c.name, hs, f.want(c.p, domain.SeqRange{}))
	}
}

// testArmSeq: SeqRange (After, *UpTo] bounds the hits; nil UpTo is unbounded and a non-nil 0 matches nothing.
func testArmSeq(t *testing.T, s store.DB, a recallArm) {
	f := newArmFixture(t, s)
	seq := func(i int) int64 { return f.units[i].Seq }
	ranges := []struct {
		name string
		r    domain.SeqRange
	}{
		{"zero range", domain.SeqRange{}},
		{"(u0, u2]", domain.SeqUpTo(seq(0), seq(2))},
		{"after u2", domain.SeqRange{After: seq(2)}},
		{"up to u1", domain.SeqUpTo(0, seq(1))},
		{"up to 0 (empty snapshot)", domain.SeqUpTo(0, 0)},
		{"after the last unit", domain.SeqRange{After: seq(len(f.units) - 1)}},
	}
	for _, c := range ranges {
		want := f.want(nil, c.r)
		if c.name == "zero range" && len(want) != len(f.units) {
			t.Fatalf("fixture: the zero range admits %d of %d units", len(want), len(f.units))
		}
		wantArmHits(t, a, a.name+" Seq "+c.name, a.search(t, s, f, nil, c.r), want)
	}
}

// ---------- Reader lifetime and tenant isolation ----------

// testReaderReuse: one Reader obtained before a commit sees the committed data on its next call.
func testReaderReuse(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	r := s.Read()
	u := fact(bankA, "written after the reader was obtained")
	u.Embedding = angleVec(0)
	us, err := r.GetUnits(t.Context(), bankA, []domain.UnitID{u.ID})
	if err != nil || len(us) != 0 {
		t.Fatalf("GetUnits before the write = %v, %v; want none", unitIDs(us), err)
	}
	seq := insertUnits(t, s, u)
	us, err = r.GetUnits(t.Context(), bankA, []domain.UnitID{u.ID})
	if err != nil {
		t.Fatalf("GetUnits on the reused Reader: %v", err)
	}
	wantSeq(t, "reused Reader GetUnits after commit", unitIDs(us), []domain.UnitID{u.ID})
	if got, err := r.BankSeq(t.Context(), bankA); err != nil || got != seq {
		t.Errorf("reused Reader BankSeq = %d, %v; want %d", got, err, seq)
	}
	hs, err := r.SearchVector(t.Context(), store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10})
	if err != nil {
		t.Fatalf("SearchVector on the reused Reader: %v", err)
	}
	wantSeq(t, "reused Reader SearchVector after commit", hitIDs(hs), []domain.UnitID{u.ID})
}

// tenantUnit builds a unit every arm finds: embedding angleVec(0), the word "zebra", dated inside armWindow.
func tenantUnit(b domain.BankRef, text string, doc domain.DocumentID) domain.Unit {
	u := dated(b, "zebra "+text, day(70), day(71))
	u.Embedding, u.DocumentID = angleVec(0), doc
	return u
}

// testTenantIsolation: the same bank id under another tenant shares no units, documents, events or seq.
func testTenantIsolation(t *testing.T, s store.DB) {
	other := bankOtherTenant
	ensureBanks(t, s, bankA, other)
	upsertDocs(t, s, bankA, "doc-1", "doc-only-a")
	upsertDocs(t, s, other, "doc-1")
	mine, mine2 := tenantUnit(bankA, "one", "doc-1"), tenantUnit(bankA, "two", "doc-1")
	theirs := tenantUnit(other, "other", "doc-1")
	insertUnits(t, s, mine)
	insertUnits(t, s, mine2)
	insertUnits(t, s, theirs)
	insertLinks(t, s, link(bankA, mine.ID, mine2.ID, graph.KindSemantic, 0.9))
	appendEvents(t, s, bankA, "event-a")
	appendEvents(t, s, other, "event-other")

	if got := getUnits(t, s, other, mine.ID, mine2.ID); len(got) != 0 {
		t.Errorf("GetUnits(other tenant, tenant-1 ids) = %v, want none", unitIDs(got))
	}
	wantSeq(t, "GetUnits(other tenant)", unitIDs(getUnits(t, s, other, theirs.ID, mine.ID)), []domain.UnitID{theirs.ID})
	assertTenantSearches(t, s, theirs.ID, mine.ID)
	_, err := getDocument(t, s, other, "doc-only-a")
	wantErrIs(t, "Documents().Get(other tenant, tenant-1 document)", err, domain.ErrNotFound)
	d, err := getDocument(t, s, other, "doc-1")
	if err != nil || d.BlobKey != blob.Key(other, "doc-1", "h0") {
		t.Errorf("Documents().Get(other tenant, doc-1) = %q, %v; want its own blob key", d.BlobKey, err)
	}
	wantSeq(t, "Events().List(other tenant)", eventSubjects(listEvents(t, s, other, 0, 10)), []string{"event-other"})
	wantSeq(t, "Unconsolidated(other tenant)", unitIDs(unconsolidated(t, s, other, nil, 10)), []domain.UnitID{theirs.ID})
	if a, o := bankSeq(t, s, bankA), bankSeq(t, s, other); a != 3 || o != 2 {
		t.Errorf("BankSeq tenant-1, other tenant = %d, %d; want 3, 2", a, o)
	}
}

// assertTenantSearches checks that every arm of the other tenant's bank returns only theirs, never mine.
func assertTenantSearches(t *testing.T, s store.DB, theirs, mine domain.UnitID) {
	t.Helper()
	other := bankOtherTenant
	want := []domain.UnitID{theirs}
	wantSeq(t, "SearchVector(other tenant)",
		hitIDs(searchVector(t, s, store.VectorQuery{Bank: other, Embedding: angleVec(0), Limit: 10})), want)
	wantSeq(t, "SearchText(other tenant)",
		hitIDs(searchText(t, s, store.TextQuery{Bank: other, Text: "zebra", Limit: 10})), want)
	wantSeq(t, "SearchTemporal(other tenant)",
		hitIDs(searchTemporal(t, s, store.TemporalQuery{Bank: other, Window: armWindow, Limit: 10})), want)
	q := graph.Query{Bank: other, Seeds: []domain.UnitID{mine}, MaxHops: 2, Limit: 10}
	wantSeq(t, "ExpandGraph(other tenant, tenant-1 seed)", hitIDs(expandGraph(t, s, q)), nil)
}
