package graphtest

import (
	"cmp"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// Fixture is one test's fresh, empty store and the graph Reader under test.
type Fixture struct {
	// DB seeds banks and units, and writes links through Tx.Graph.
	DB store.DB
	// Graph is the Reader under test; it must see every committed Tx.Graph write (after a sync, for an adapter
	// that syncs).
	Graph graph.Reader
}

// The banks the cases use: two banks of one tenant and a bank of another tenant with the same bank id.
var (
	bankA     = domain.BankRef{Tenant: "tenant-1", Bank: "a"}
	bankB     = domain.BankRef{Tenant: "tenant-1", Bank: "b"}
	bankOther = domain.BankRef{Tenant: "tenant-2", Bank: "a"}
)

// Run runs the contract suite: each case gets its own fixture from open and runs in parallel with the others. It
// covers expansion by hops (scores, seeds never hits, unlinked seeds), re-adding an edge, the kinds filter, the
// limit, self-links, bank and tenant isolation, Degree inside and outside a transaction, and DeleteUnitLinks.
func Run(t *testing.T, open func(t *testing.T) Fixture) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, f Fixture)
	}{
		{"ExpandHops", testExpandHops},
		{"AddLinksKeepsOneEdge", testAddLinksIdempotent},
		{"ExpandLimit", testExpandLimit},
		{"SelfLinkSkipped", testSelfLink},
		{"BankIsolation", testBankIsolation},
		{"Degree", testDegree},
		{"DeleteUnitLinks", testDeleteUnitLinks},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.fn(t, open(t))
		})
	}
}

// testExpandHops: seed -> a -> b -> c (plus a -> seed); one hop reaches a, two hops a and b, best score first;
// seeds and unlinked units are never hits.
func testExpandHops(t *testing.T, f Fixture) {
	us := units(t, f, bankA, 5)
	seed, a, b, c, lone := us[0], us[1], us[2], us[3], us[4]
	addLinks(t, f,
		link(bankA, seed, a, graph.KindSemantic, 0.9),
		link(bankA, a, seed, graph.KindSemantic, 0.9),
		link(bankA, a, b, graph.KindTemporal, 0.8),
		link(bankA, b, c, graph.KindCausal, 0.7),
	)
	q := graph.Query{Bank: bankA, Seeds: []domain.UnitID{seed}, MaxHops: 1, Limit: 10}
	wantSet(t, "1 hop", hitIDs(expand(t, f, q)), a)
	q.MaxHops = 2
	hs := expand(t, f, q)
	wantSet(t, "2 hops", hitIDs(hs), a, b)
	for i := 1; i < len(hs); i++ {
		if hs[i].Score > hs[i-1].Score {
			t.Errorf("2 hops: hit %d score %v > hit %d score %v", i, hs[i].Score, i-1, hs[i-1].Score)
		}
	}
	if len(hs) == 2 && (hs[0].Score != 0.9 || hs[1].Score < 0.72-1e-9 || hs[1].Score > 0.72+1e-9) {
		t.Errorf("2 hops: scores %v, want [0.9 0.72] (the best product of weights along a path)", hs)
	}
	q = graph.Query{Bank: bankA, Seeds: []domain.UnitID{seed, a}, MaxHops: 1, Limit: 10}
	wantSet(t, "seeds {seed, a}", hitIDs(expand(t, f, q)), b)
	q = graph.Query{Bank: bankA, Seeds: []domain.UnitID{lone}, MaxHops: 2, Limit: 10}
	wantSet(t, "an unlinked seed", hitIDs(expand(t, f, q)))
}

// testAddLinksIdempotent: re-adding an edge keeps one edge with the new weight; Kinds limits the links followed.
func testAddLinksIdempotent(t *testing.T, f Fixture) {
	us := units(t, f, bankA, 3)
	seed, a, b := us[0], us[1], us[2]
	addLinks(t, f, link(bankA, seed, a, graph.KindSemantic, 0.5), link(bankA, a, b, graph.KindCausal, 1))
	q := graph.Query{Bank: bankA, Seeds: []domain.UnitID{seed}, MaxHops: 2, Limit: 10}
	wantSet(t, "after AddLinks", hitIDs(expand(t, f, q)), a, b)
	addLinks(t, f, link(bankA, seed, a, graph.KindSemantic, 0.9))
	q.MaxHops = 1
	hs := expand(t, f, q)
	if len(hs) != 1 || hs[0].ID != a || hs[0].Score != 0.9 {
		t.Errorf("after re-adding seed -> a at 0.9: hits %v, want one hit on a scoring 0.9", hs)
	}
	if n := degree(t, f, seed, graph.KindSemantic); n != 1 {
		t.Errorf("Degree(seed, semantic) after re-adding = %d, want 1", n)
	}
	q.Kinds = []graph.Kind{graph.KindTemporal}
	wantSet(t, "Kinds=[temporal]", hitIDs(expand(t, f, q)))
	q.Kinds, q.MaxHops = []graph.Kind{graph.KindSemantic}, 2
	wantSet(t, "Kinds=[semantic], 2 hops (a -> b is causal)", hitIDs(expand(t, f, q)), a)
}

// testExpandLimit: Limit caps the hits, keeping the best; Limit <= 0 and no seeds return none.
func testExpandLimit(t *testing.T, f Fixture) {
	us := units(t, f, bankA, 4)
	seed := us[0]
	addLinks(t, f, link(bankA, seed, us[1], graph.KindSemantic, 0.9), link(bankA, seed, us[2], graph.KindSemantic, 0.8),
		link(bankA, seed, us[3], graph.KindSemantic, 0.7))
	q := graph.Query{Bank: bankA, Seeds: []domain.UnitID{seed}, MaxHops: 1, Limit: 2}
	if got := hitIDs(expand(t, f, q)); !slices.Equal(got, []domain.UnitID{us[1], us[2]}) {
		t.Errorf("Limit 2: %v, want the two best %v", got, us[1:3])
	}
	q.Limit = 0
	wantSet(t, "Limit 0", hitIDs(expand(t, f, q)))
	q = graph.Query{Bank: bankA, MaxHops: 1, Limit: 10}
	wantSet(t, "no seeds", hitIDs(expand(t, f, q)))
}

// testSelfLink: a self-link is skipped, so it never counts toward the unit's degree.
func testSelfLink(t *testing.T, f Fixture) {
	u := units(t, f, bankA, 1)[0]
	addLinks(t, f, link(bankA, u, u, graph.KindSemantic, 1))
	if n := degree(t, f, u, graph.KindSemantic); n != 0 {
		t.Errorf("Degree after a self-link = %d, want 0", n)
	}
}

// testBankIsolation: an expansion never crosses into another bank, of the same tenant or another tenant with the
// same bank id, even from a seed of that bank.
func testBankIsolation(t *testing.T, f Fixture) {
	mine := units(t, f, bankA, 2)
	sibling := units(t, f, bankB, 2)
	theirs := units(t, f, bankOther, 2)
	addLinks(t, f, link(bankA, mine[0], mine[1], graph.KindSemantic, 1))
	addLinks(t, f, link(bankB, sibling[0], sibling[1], graph.KindSemantic, 1))
	addLinks(t, f, link(bankOther, theirs[0], theirs[1], graph.KindSemantic, 1))
	for _, b := range []domain.BankRef{bankB, bankOther} {
		q := graph.Query{Bank: b, Seeds: []domain.UnitID{mine[0]}, MaxHops: 2, Limit: 10}
		wantSet(t, "bank "+b.Tenant+"/"+b.Bank+" from a bank-A seed", hitIDs(expand(t, f, q)))
	}
	q := graph.Query{Bank: bankOther, Seeds: []domain.UnitID{theirs[0]}, MaxHops: 2, Limit: 10}
	wantSet(t, "the other tenant's own seed", hitIDs(expand(t, f, q)), theirs[1])
}

// testDegree: Degree counts the links of one kind touching a unit in either direction, and inside a transaction it
// sees that transaction's own writes.
func testDegree(t *testing.T, f Fixture) {
	us := units(t, f, bankA, 3)
	u, v, w := us[0], us[1], us[2]
	addLinks(t, f, link(bankA, u, v, graph.KindSemantic, 1), link(bankA, v, u, graph.KindSemantic, 1),
		link(bankA, w, u, graph.KindTemporal, 1))
	if n := degree(t, f, u, graph.KindSemantic); n != 2 {
		t.Errorf("Degree(u, semantic) = %d, want 2 (both directions)", n)
	}
	if n := degree(t, f, u, graph.KindTemporal); n != 1 {
		t.Errorf("Degree(u, temporal) = %d, want 1", n)
	}
	var inTx int
	err := f.DB.InTx(t.Context(), func(tx store.Tx) error {
		if err := tx.Graph().AddLinks(t.Context(), []graph.Link{link(bankA, u, w, graph.KindTemporal, 1)}); err != nil {
			return err
		}
		var err error
		inTx, err = tx.Graph().Degree(t.Context(), u, graph.KindTemporal)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if inTx != 2 {
		t.Errorf("Degree(u, temporal) inside the writing transaction = %d, want 2", inTx)
	}
}

// testDeleteUnitLinks: DeleteUnitLinks removes the given kinds touching the unit in both directions (empty = all)
// and reports how many.
func testDeleteUnitLinks(t *testing.T, f Fixture) {
	us := units(t, f, bankA, 2)
	u, v := us[0], us[1]
	addLinks(t, f, link(bankA, u, v, graph.KindSemantic, 0.9), link(bankA, v, u, graph.KindSemantic, 0.8),
		link(bankA, u, v, graph.KindTemporal, 0.7), link(bankA, v, u, graph.KindCausal, 1))
	if n := deleteUnitLinks(t, f, u, graph.KindSemantic); n != 2 {
		t.Errorf("DeleteUnitLinks(semantic) = %d, want 2 (both directions)", n)
	}
	if sem, tmp := degree(t, f, u, graph.KindSemantic), degree(t, f, u, graph.KindTemporal); sem != 0 || tmp != 1 {
		t.Errorf("after DeleteUnitLinks(semantic): %d semantic, %d temporal; want 0, 1", sem, tmp)
	}
	if n := deleteUnitLinks(t, f, u); n != 2 {
		t.Errorf("DeleteUnitLinks(all) = %d, want 2", n)
	}
	q := graph.Query{Bank: bankA, Seeds: []domain.UnitID{v}, MaxHops: 1, Limit: 10}
	wantSet(t, "expand after deleting every link", hitIDs(expand(t, f, q)))
}

// units creates bank b when missing and inserts n world facts in one transaction stamped with its seq, returning
// their ids in insertion order.
func units(t *testing.T, f Fixture, b domain.BankRef, n int) []domain.UnitID {
	t.Helper()
	ids := make([]domain.UnitID, n)
	err := f.DB.InTx(t.Context(), func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
			return err
		}
		seq, err := tx.Seq(t.Context(), b)
		if err != nil {
			return err
		}
		us := make([]domain.Unit, n)
		for i := range us {
			ids[i] = domain.UnitID(uuid.NewString())
			us[i] = domain.Unit{ID: ids[i], Bank: b, Type: domain.FactTypeWorld, Text: "unit " + string(ids[i]), Seq: seq}
		}
		return tx.Units().Insert(t.Context(), us)
	})
	if err != nil {
		t.Fatalf("insert units: %v", err)
	}
	return ids
}

// link builds a link of bank b.
func link(b domain.BankRef, from, to domain.UnitID, kind graph.Kind, w float64) graph.Link {
	return graph.Link{Bank: b, From: from, To: to, Kind: kind, Weight: w}
}

// addLinks stores ls through Tx.Graph in one transaction.
func addLinks(t *testing.T, f Fixture, ls ...graph.Link) {
	t.Helper()
	if err := f.DB.InTx(t.Context(), func(tx store.Tx) error { return tx.Graph().AddLinks(t.Context(), ls) }); err != nil {
		t.Fatalf("AddLinks: %v", err)
	}
}

// deleteUnitLinks runs DeleteUnitLinks in one transaction.
func deleteUnitLinks(t *testing.T, f Fixture, unit domain.UnitID, kinds ...graph.Kind) int {
	t.Helper()
	var n int
	err := f.DB.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		n, err = tx.Graph().DeleteUnitLinks(t.Context(), unit, kinds)
		return err
	})
	if err != nil {
		t.Fatalf("DeleteUnitLinks: %v", err)
	}
	return n
}

// expand runs Expand on the Reader under test.
func expand(t *testing.T, f Fixture, q graph.Query) []graph.Hit {
	t.Helper()
	hs, err := f.Graph.Expand(t.Context(), q)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return hs
}

// degree runs Degree on the Reader under test.
func degree(t *testing.T, f Fixture, unit domain.UnitID, kind graph.Kind) int {
	t.Helper()
	n, err := f.Graph.Degree(t.Context(), unit, kind)
	if err != nil {
		t.Fatalf("Degree: %v", err)
	}
	return n
}

// hitIDs returns the ids of hits in order.
func hitIDs(hs []graph.Hit) []domain.UnitID {
	out := make([]domain.UnitID, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

// wantSet fails the test unless got holds exactly want, in any order.
func wantSet(t *testing.T, what string, got []domain.UnitID, want ...domain.UnitID) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.SortFunc(g, cmp.Compare)
	slices.SortFunc(w, cmp.Compare)
	if !slices.Equal(g, w) {
		t.Errorf("%s: got %v, want (any order) %v", what, g, w)
	}
}
