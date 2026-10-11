package domain

import (
	"slices"
	"testing"
)

// TestSelectDirectives pins the one directive rule: inactive directives never apply, untagged ones always do, and a
// tagged one applies only to a tag filter its tags satisfy (flat tags under the filter's mode, then every group).
func TestSelectDirectives(t *testing.T) {
	stored := []Directive{
		{ID: "global", Rules: []string{"g"}, Active: true},
		{ID: "a", Rules: []string{"a"}, Tags: []string{"team-a"}, Active: true},
		{ID: "ab", Rules: []string{"ab"}, Tags: []string{"team-a", "team-b"}, Active: true},
		{ID: "off", Rules: []string{"off"}, Active: false},
	}
	group := TagGroup{Kind: TagGroupLeaf, Leaf: &TagLeaf{Tags: []string{"team-b"}}}
	cases := []struct {
		name     string
		filter   TagFilter
		applyAll bool
		want     []DirectiveID
	}{
		{"untagged request: global only", TagFilter{}, false, []DirectiveID{"global"}},
		{"exact with no tags: global only", TagFilter{Match: TagsMatchExact}, false, []DirectiveID{"global"}},
		{"any team-a", TagFilter{Tags: []string{"Team-A"}}, false, []DirectiveID{"global", "a", "ab"}},
		{
			"all team-a team-b",
			TagFilter{Tags: []string{"team-a", "team-b"}, Match: TagsMatchAll},
			false,
			[]DirectiveID{"global", "ab"},
		},
		{
			"all_strict team-a (a mental model tagged team-a)",
			TagFilter{Tags: []string{"team-a"}, Match: TagsMatchAllStrict},
			false,
			[]DirectiveID{"global", "a", "ab"},
		},
		{
			"all_strict team-a team-c: partial overlap does not apply",
			TagFilter{Tags: []string{"team-a", "team-c"}, Match: TagsMatchAllStrict},
			false,
			[]DirectiveID{"global"},
		},
		{"group team-b", TagFilter{Groups: []TagGroup{group}}, false, []DirectiveID{"global", "ab"}},
		{"apply all", TagFilter{}, true, []DirectiveID{"global", "a", "ab"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []DirectiveID
			for _, d := range SelectDirectives(stored, tc.filter, tc.applyAll) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
