package domain

import (
	"errors"
	"slices"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{" B ", "a", "A", "", "  ", "b"})
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("NormalizeTags = %q, want %q", got, want)
	}
	if got := NormalizeTags(nil); got == nil || len(got) != 0 {
		t.Fatalf("NormalizeTags(nil) = %#v, want empty non-nil", got)
	}
	if !SameTagSet([]string{"x", "Y"}, []string{"y", "x", "x"}) {
		t.Fatal("SameTagSet should ignore order, case and duplicates")
	}
}

func TestMatchTags(t *testing.T) {
	cases := []struct {
		unit, filter []string
		mode         TagsMatch
		want         bool
	}{
		{nil, []string{"a"}, TagsMatchAny, true},
		{nil, []string{"a"}, TagsMatchAll, true},
		{nil, []string{"a"}, TagsMatchAnyStrict, false},
		{nil, []string{"a"}, TagsMatchAllStrict, false},
		{[]string{"a", "b"}, []string{"b", "c"}, TagsMatchAny, true},
		{[]string{"a", "b"}, []string{"b", "c"}, TagsMatchAll, false},
		{[]string{"a", "b", "c"}, []string{"b", "c"}, TagsMatchAllStrict, true},
		{[]string{"x"}, []string{"b"}, TagsMatchAnyStrict, false},
		{[]string{"a"}, []string{"a", "b"}, TagsMatchExact, false},
		{[]string{"b", "a"}, []string{"a", "b"}, TagsMatchExact, true},
		{nil, nil, TagsMatchExact, true},
		{[]string{"a"}, nil, TagsMatchExact, false},
		{[]string{"a"}, nil, TagsMatchAnyStrict, true},
		{[]string{"a"}, []string{"A"}, TagsMatchUnspecified, true},
	}
	for i, tc := range cases {
		if got := MatchTags(tc.unit, tc.filter, tc.mode); got != tc.want {
			t.Errorf("case %d MatchTags(%q, %q, %q) = %v, want %v", i, tc.unit, tc.filter, tc.mode, got, tc.want)
		}
	}
}

func TestMatchGroup(t *testing.T) {
	leaf := func(m TagsMatch, tags ...string) TagGroup {
		return TagGroup{Kind: TagGroupLeaf, Leaf: &TagLeaf{Tags: tags, Match: m}}
	}
	notB := leaf("", "b")
	g := TagGroup{Kind: TagGroupAllOf, Groups: []TagGroup{
		leaf("", "a"),
		{Kind: TagGroupNoneOf, Not: &notB},
	}}
	if !MatchGroup([]string{"a"}, g) || MatchGroup([]string{"a", "b"}, g) || MatchGroup(nil, g) {
		t.Fatal("all_of(a, none_of(b)) evaluated wrongly")
	}
	anyOf := TagGroup{Kind: TagGroupAnyOf, Groups: []TagGroup{leaf("", "x"), leaf(TagsMatchAll, "y", "z")}}
	if !MatchGroup([]string{"z", "y"}, anyOf) || MatchGroup([]string{"y"}, anyOf) {
		t.Fatal("any_of evaluated wrongly")
	}
	f := TagFilter{Groups: []TagGroup{leaf("", "a"), leaf("", "c")}}
	if MatchFilter([]string{"a"}, f) || !MatchFilter([]string{"a", "c"}, f) {
		t.Fatal("top-level groups must be AND-ed")
	}
}

func TestNormalizeTagNFC(t *testing.T) {
	got := NormalizeTags([]string{"é", "é", "É", "É"})
	if want := []string{"é"}; !slices.Equal(got, want) {
		t.Fatalf("NormalizeTags = %q, want %q (NFC, lowercased, one tag)", got, want)
	}
}

func TestEmptyAndMalformedGroups(t *testing.T) {
	cases := []struct {
		name string
		g    TagGroup
		want bool
	}{
		{"empty all_of", TagGroup{Kind: TagGroupAllOf}, true},
		{"empty any_of", TagGroup{Kind: TagGroupAnyOf}, false},
		{"leaf without leaf", TagGroup{Kind: TagGroupLeaf}, false},
		{"none_of without child", TagGroup{Kind: TagGroupNoneOf}, false},
		{"unknown kind", TagGroup{Kind: "bogus"}, false},
	}
	for _, tc := range cases {
		if got := MatchGroup([]string{"a"}, tc.g); got != tc.want {
			t.Errorf("%s: MatchGroup = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTagFilterValidate(t *testing.T) {
	leaf := TagGroup{Kind: TagGroupLeaf, Leaf: &TagLeaf{Tags: []string{"a"}}}
	both := TagFilter{Tags: []string{"a"}, Groups: []TagGroup{leaf}}
	if err := both.Validate(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("tags + groups: err = %v, want ErrInvalidArgument", err)
	}
	bad := TagFilter{Groups: []TagGroup{{Kind: TagGroupNoneOf}}}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("malformed group: err = %v, want ErrInvalidArgument", err)
	}
	if err := (TagFilter{Groups: []TagGroup{leaf}}).Validate(); err != nil {
		t.Fatalf("valid filter: %v", err)
	}
	// MatchFilter never drops a part: both parts must hold.
	f := TagFilter{Tags: []string{"b"}, Match: TagsMatchAnyStrict, Groups: []TagGroup{leaf}}
	if MatchFilter([]string{"a"}, f) || !MatchFilter([]string{"a", "b"}, f) {
		t.Fatal("MatchFilter must AND flat tags and groups")
	}
}
