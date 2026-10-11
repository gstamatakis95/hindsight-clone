package armtest

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/recall"
	"memd/internal/store"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Fixture is one test's fresh, empty store and the arm under test.
type Fixture struct {
	// DB is the store the suite seeds; the arm must read it.
	DB store.DB
	// Arm is the arm under test, built over DB.
	Arm recall.Arm
}

// The banks the suite seeds: two banks of one tenant and a bank of another tenant with the same bank id.
var (
	bankA     = domain.BankRef{Tenant: "armtest-1", Bank: "a"}
	bankB     = domain.BankRef{Tenant: "armtest-1", Bank: "b"}
	bankOther = domain.BankRef{Tenant: "armtest-2", Bank: "a"}
)

// perBank is how many matching units and how many linked-only units each bank gets; limit is the query's Limit,
// below both, so the limit binds for every arm.
const (
	perBank = 5
	limit   = 3
)

// now anchors the seeded occurrence times and the query window.
var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// Run runs the contract suite against the fixture open returns.
func Run(t *testing.T, open func(t *testing.T) Fixture) {
	t.Helper()
	f := open(t)
	units := map[domain.BankRef]map[domain.UnitID]bool{}
	for _, b := range []domain.BankRef{bankA, bankB, bankOther} {
		units[b] = seed(t, f.DB, b)
	}
	q := recall.Query{
		Bank: bankA, Text: "rocket launch", Embedding: axis(0), Limit: limit, Budget: domain.BudgetMid,
		Window: &domain.TimeWindow{Start: now.AddDate(0, 0, -7), End: now.AddDate(0, 0, 1)},
	}
	t.Run("LimitAndOrder", func(t *testing.T) {
		hs := retrieve(t, f.Arm, q)
		if len(hs) == 0 || len(hs) > limit {
			t.Fatalf("%s: %d hits, want 1..%d", f.Arm.Name(), len(hs), limit)
		}
		for i := 1; i < len(hs); i++ {
			if hs[i].Score > hs[i-1].Score {
				t.Errorf("%s: hit %d scores %v above hit %d's %v", f.Arm.Name(), i, hs[i].Score, i-1, hs[i-1].Score)
			}
		}
	})
	t.Run("StaysInBank", func(t *testing.T) {
		for _, b := range []domain.BankRef{bankA, bankB, bankOther} {
			bq := q
			bq.Bank = b
			for _, h := range retrieve(t, f.Arm, bq) {
				if !units[b][h.ID] {
					t.Errorf("%s: a query on %v returned %s, which is not a unit of that bank", f.Arm.Name(), b, h.ID)
				}
			}
		}
	})
	t.Run("NeverWrites", func(t *testing.T) {
		before := seqs(t, f.DB)
		retrieve(t, f.Arm, q)
		if after := seqs(t, f.DB); after != before {
			t.Errorf("%s: bank seqs moved from %v to %v", f.Arm.Name(), before, after)
		}
	})
	t.Run("HonoursCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.Arm.Retrieve(ctx, q); err == nil || errors.Is(err, recall.ErrSkip) {
			t.Errorf("%s: Retrieve with a cancelled context = %v, want an error", f.Arm.Name(), err)
		}
	})
}

// retrieve runs the arm and fails the test on error.
func retrieve(t *testing.T, a recall.Arm, q recall.Query) []recall.Hit {
	t.Helper()
	hs, err := a.Retrieve(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: Retrieve: %v", a.Name(), err)
	}
	return hs
}

// seed writes b's units and links in one transaction and returns the unit ids: perBank units matching the query
// (text "rocket launch", the query's embedding, occurred inside the window) and perBank units reachable from
// them only over semantic links (other text, an orthogonal embedding).
func seed(t *testing.T, db store.DB, b domain.BankRef) map[domain.UnitID]bool {
	t.Helper()
	ids := map[domain.UnitID]bool{}
	err := db.InTx(t.Context(), func(tx store.Tx) error {
		clear(ids)
		if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
			return err
		}
		seq, err := tx.Seq(t.Context(), b)
		if err != nil {
			return err
		}
		us, links := units(b, seq)
		for _, u := range us {
			ids[u.ID] = true
		}
		if err := tx.Units().Insert(t.Context(), us); err != nil {
			return err
		}
		return tx.Graph().AddLinks(t.Context(), links)
	})
	if err != nil {
		t.Fatalf("seed %v: %v", b, err)
	}
	return ids
}

// units builds b's seeded units and the semantic links from each matching unit to one linked-only unit.
func units(b domain.BankRef, seq int64) ([]domain.Unit, []graph.Link) {
	var us []domain.Unit
	var links []graph.Link
	for i := range perBank {
		at := now.AddDate(0, 0, -i)
		match := domain.Unit{
			ID: domain.UnitID(uuid.NewString()), Bank: b, Type: domain.FactTypeWorld, Text: "rocket launch day",
			Tags: []string{}, Seq: seq, Embedding: axis(0), OccurredStart: &at,
		}
		linked := domain.Unit{
			ID: domain.UnitID(uuid.NewString()), Bank: b, Type: domain.FactTypeWorld, Text: "unrelated notes",
			Tags: []string{}, Seq: seq, Embedding: axis(1),
		}
		us = append(us, match, linked)
		links = append(links, graph.Link{
			Bank: b, From: match.ID, To: linked.ID, Kind: graph.KindSemantic, Weight: 1 - float64(i)/10,
		})
	}
	return us, links
}

// axis is the unit embedding along dimension i.
func axis(i int) []float32 {
	v := make([]float32, domain.EmbeddingDim)
	v[i] = 1
	return v
}

// seqs reads the three banks' change sequences.
func seqs(t *testing.T, db store.DB) [3]int64 {
	t.Helper()
	var out [3]int64
	for i, b := range []domain.BankRef{bankA, bankB, bankOther} {
		s, err := db.Read().BankSeq(t.Context(), b)
		if err != nil {
			t.Fatalf("BankSeq %v: %v", b, err)
		}
		out[i] = s
	}
	return out
}
