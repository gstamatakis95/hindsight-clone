package postgres_test

import (
	"math/rand/v2"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
)

// predicateUnitTags are the (normalized, as stored) tag sets every rendered predicate is evaluated against.
var predicateUnitTags = [][]string{
	{}, {"alpha"}, {"beta"}, {"gamma"}, {"alpha", "beta"}, {"alpha", "gamma"}, {"alpha", "beta", "gamma"},
}

// predicateModes are the leaf modes the random trees draw from, including the unspecified and an unknown one.
var predicateModes = []domain.TagsMatch{
	domain.TagsMatchUnspecified, domain.TagsMatchAny, domain.TagsMatchAll, domain.TagsMatchExact,
	domain.TagsMatchAnyStrict, domain.TagsMatchAllStrict, "bogus",
}

// predicateLeafTags are filter tag lists, some not normalized, so the renderer's normalization is exercised.
var predicateLeafTags = [][]string{
	nil, {}, {"alpha"}, {" Alpha "}, {"beta"}, {"alpha", "beta"}, {"BETA", "beta", "gamma"}, {""}, {"delta"},
}

// randomPredicate builds a random tree of the given depth, including malformed nodes.
func randomPredicate(r *rand.Rand, depth int) store.SQLPredicate {
	if depth == 0 || r.IntN(3) == 0 {
		return store.SQLPredicate{
			Op:    store.PredicateTags,
			Tags:  predicateLeafTags[r.IntN(len(predicateLeafTags))],
			Match: predicateModes[r.IntN(len(predicateModes))],
		}
	}
	ops := []store.PredicateOp{store.PredicateAnd, store.PredicateOr, store.PredicateNot, "xor"}
	p := store.SQLPredicate{Op: ops[r.IntN(len(ops))]}
	n := r.IntN(4)
	if p.Op == store.PredicateNot && r.IntN(4) != 0 {
		n = 1 // mostly well-formed nots
	}
	for range n {
		p.Args = append(p.Args, randomPredicate(r, depth-1))
	}
	return p
}

// TestRenderPredicateMatchesEval renders many predicates and checks Postgres selects exactly the tag sets
// (*store.SQLPredicate).Eval accepts.
func TestRenderPredicateMatchesEval(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test data, not security
	preds := []*store.SQLPredicate{nil, {Op: store.PredicateAnd}, {Op: store.PredicateOr}, {Op: store.PredicateNot}}
	for range 300 {
		p := randomPredicate(r, 3)
		preds = append(preds, &p)
	}
	for i, p := range preds {
		sql, args := postgres.PredicateQuery(p, predicateUnitTags)
		var got []int32
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(&got); err != nil {
			t.Fatalf("predicate %d %+v: %v\n%s", i, p, err, sql)
		}
		var want []int32
		for j, tags := range predicateUnitTags {
			if p.Eval(tags) {
				want = append(want, int32(j)) //nolint:gosec // j < len(predicateUnitTags)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("predicate %d %+v: SQL selects %v, Eval %v\n%s", i, p, got, want, sql)
		}
	}
}
