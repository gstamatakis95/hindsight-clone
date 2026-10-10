package reflect

import (
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

func TestSelectDirectives(t *testing.T) {
	stored := []domain.Directive{
		{ID: "global", Rules: []string{"g"}, Active: true},
		{ID: "a", Rules: []string{"a"}, Tags: []string{"team-a"}, Active: true},
		{ID: "ab", Rules: []string{"ab"}, Tags: []string{"team-a", "team-b"}, Active: true},
		{ID: "off", Rules: []string{"off"}, Active: false},
	}
	extra := []domain.Directive{{Name: "extra", Rules: []string{"e"}}}
	group := domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"team-b"}}}
	cases := []struct {
		name     string
		filter   domain.TagFilter
		applyAll bool
		extra    []domain.Directive
		want     []port.DirectiveID
	}{
		{"untagged request: global only", domain.TagFilter{}, false, nil, []port.DirectiveID{"global"}},
		{"any team-a", domain.TagFilter{Tags: []string{"Team-A"}}, false, nil, []port.DirectiveID{"global", "a", "ab"}},
		{
			"all team-a team-b",
			domain.TagFilter{Tags: []string{"team-a", "team-b"}, Match: domain.TagsMatchAll},
			false,
			nil,
			[]port.DirectiveID{"global", "ab"},
		},
		{
			"group team-b",
			domain.TagFilter{Groups: []domain.TagGroup{group}},
			false, nil,
			[]port.DirectiveID{"global", "ab"},
		},
		{"apply all", domain.TagFilter{}, true, nil, []port.DirectiveID{"global", "a", "ab"}},
		{"extras added, stored kept", domain.TagFilter{}, false, extra, []port.DirectiveID{"global", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []port.DirectiveID
			for _, d := range selectDirectives(stored, tc.filter, tc.applyAll, tc.extra) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidCitations(t *testing.T) {
	st := &AgentState{
		RetrievedUnits:  map[port.UnitID]domain.Unit{"u1": {ID: "u1"}, "o1": {ID: "o1"}},
		RetrievedModels: map[port.MentalModelID]struct{}{"mm": {}},
	}
	got := validCitations(st, []string{"u1", "x", "mm", "u1", "o1", "mm-never"})
	if !slices.Equal(got, []string{"u1", "mm", "o1"}) {
		t.Errorf("got %v", got)
	}
}

func TestDocumentMarkdown(t *testing.T) {
	d := doneDocument{Sections: []docSection{
		{Heading: "", Level: 2, Blocks: []string{"Intro."}},
		{Heading: "Deep", Level: 9, Blocks: []string{" A ", ""}},
		{Heading: "Top", Level: 0, Blocks: nil},
	}}
	want := "Intro.\n\n###### Deep\n\nA\n\n# Top"
	if got := d.markdown(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsContextOverflow(t *testing.T) {
	for msg, want := range map[string]bool{
		"unavailable: gateway: status 400: This model's maximum context length is 8192 tokens": true,
		"error code: context_length_exceeded":                                                  true,
		"prompt is too long: 210000 tokens > 200000 maximum":                                   true,
		"unavailable: gateway: status 429: too many tokens per minute":                         false,
		"unavailable: gateway: status 503":                                                     false,
	} {
		if got := isContextOverflow(errString(msg)); got != want {
			t.Errorf("%q: got %v", msg, got)
		}
	}
}

// errString is an error with a fixed message.
type errString string

func (e errString) Error() string { return string(e) }
