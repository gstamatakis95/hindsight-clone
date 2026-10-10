package port

import (
	"memd/internal/domain"
	"testing"
)

func TestSQLPredicateEval(t *testing.T) {
	leaf := func(m domain.TagsMatch, tags ...string) SQLPredicate {
		return SQLPredicate{Op: PredicateTags, Tags: tags, Match: m}
	}
	p := &SQLPredicate{Op: PredicateAnd, Args: []SQLPredicate{
		leaf(domain.TagsMatchAnyStrict, "a", "b"),
		{Op: PredicateNot, Args: []SQLPredicate{leaf(domain.TagsMatchAnyStrict, "x")}},
	}}
	cases := []struct {
		tags []string
		want bool
	}{
		{[]string{"a"}, true},
		{[]string{"b", "c"}, true},
		{[]string{"a", "x"}, false},
		{nil, false},
		{[]string{"c"}, false},
	}
	for _, tc := range cases {
		if got := p.Eval(tc.tags); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.tags, got, tc.want)
		}
	}
	var none *SQLPredicate
	if !none.Eval([]string{"anything"}) {
		t.Error("a nil predicate must match everything")
	}
	if (&SQLPredicate{Op: PredicateOr}).Eval([]string{"a"}) {
		t.Error("an empty or must be false")
	}
	if !(&SQLPredicate{Op: PredicateAnd}).Eval(nil) {
		t.Error("an empty and must be true")
	}
}

func TestSeqRangeContains(t *testing.T) {
	cases := []struct {
		r    SeqRange
		seq  int64
		want bool
	}{
		{SeqRange{}, 1, true},
		{SeqRange{After: 5}, 5, false},
		{SeqRange{After: 5}, 6, true},
		{SeqRange{UpTo: 5}, 5, true},
		{SeqRange{UpTo: 5}, 6, false},
		{SeqRange{After: 2, UpTo: 4}, 3, true},
	}
	for _, tc := range cases {
		if got := tc.r.Contains(tc.seq); got != tc.want {
			t.Errorf("%+v.Contains(%d) = %v, want %v", tc.r, tc.seq, got, tc.want)
		}
	}
}
