package domain

import "testing"

func TestTagPredicateEval(t *testing.T) {
	leaf := func(m TagsMatch, tags ...string) TagPredicate {
		return TagPredicate{Op: PredicateTags, Tags: tags, Match: m}
	}
	p := &TagPredicate{Op: PredicateAnd, Args: []TagPredicate{
		leaf(TagsMatchAnyStrict, "a", "b"),
		{Op: PredicateNot, Args: []TagPredicate{leaf(TagsMatchAnyStrict, "x")}},
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
	var none *TagPredicate
	if !none.Eval([]string{"anything"}) {
		t.Error("a nil predicate must match everything")
	}
	if (&TagPredicate{Op: PredicateOr}).Eval([]string{"a"}) {
		t.Error("an empty or must be false")
	}
	if !(&TagPredicate{Op: PredicateAnd}).Eval(nil) {
		t.Error("an empty and must be true")
	}
	if (&TagPredicate{Op: PredicateNot}).Eval(nil) || (&TagPredicate{Op: "bogus"}).Eval(nil) {
		t.Error("malformed nodes must match nothing")
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
		{SeqUpTo(0, 5), 5, true},
		{SeqUpTo(0, 5), 6, false},
		{SeqUpTo(2, 4), 3, true},
		{SeqUpTo(0, 0), 1, false},
	}
	for _, tc := range cases {
		if got := tc.r.Contains(tc.seq); got != tc.want {
			t.Errorf("%+v.Contains(%d) = %v, want %v", tc.r, tc.seq, got, tc.want)
		}
	}
}
