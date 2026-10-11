package recall

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// stubVocab is a TagVocabulary over a fixed list, most used first: it reports descending counts in byte
// order of the tags (as TagCounts does), so the compiler's count sort must restore the list's order.
type stubVocab []string

func (v stubVocab) TagCounts(
	context.Context, domain.BankRef, domain.TagSource, string, string, int,
) ([]domain.TagCount, error) {
	out := make([]domain.TagCount, len(v))
	for i, t := range v {
		out[i] = domain.TagCount{Tag: t, Count: int64(len(v) - i)}
	}
	slices.SortFunc(out, func(a, b domain.TagCount) int { return strings.Compare(a.Tag, b.Tag) })
	return out, nil
}

// countingVocab is a TagVocabulary that counts its reads and can fail.
type countingVocab struct {
	tags  []string
	err   error
	calls int
}

func (v *countingVocab) TagCounts(
	ctx context.Context, b domain.BankRef, s domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	v.calls++
	if v.err != nil {
		return nil, v.err
	}
	return stubVocab(v.tags).TagCounts(ctx, b, s, prefix, after, limit)
}

var tagsBank = domain.BankRef{Tenant: "t", Bank: "b"}

// unitTagSets covers untagged, overlapping, containing, exact, disjoint and un-normalized units against
// filters over {a, b}.
var unitTagSets = [][]string{
	nil,
	{"a"},
	{"a", "x"},
	{"a", "b"},
	{"A ", "b"},
	{"a", "b", "c"},
	{"b", "c"},
	{"x"},
	{"x", "y"},
}

func mustCompile(t *testing.T, v TagVocabulary, f domain.TagFilter, minSim float64) store.SQLPredicate {
	t.Helper()
	p, err := NewTagFilterCompiler(v).Compile(context.Background(), tagsBank, f, minSim)
	if err != nil {
		t.Fatalf("Compile(%+v): %v", f, err)
	}
	return p
}

// checkLeaves fails when any leaf has no explicit match or carries un-normalized tags.
func checkLeaves(t *testing.T, p store.SQLPredicate) {
	t.Helper()
	if p.Op == store.PredicateTags {
		if p.Match == domain.TagsMatchUnspecified {
			t.Fatalf("leaf %+v has no explicit match", p)
		}
		if !slices.Equal(p.Tags, domain.NormalizeTags(p.Tags)) {
			t.Fatalf("leaf tags %q are not normalized", p.Tags)
		}
	}
	for _, a := range p.Args {
		checkLeaves(t, a)
	}
}

func TestCompileFlatTags(t *testing.T) {
	t.Parallel()
	modes := []domain.TagsMatch{
		domain.TagsMatchUnspecified, domain.TagsMatchAny, domain.TagsMatchAll, domain.TagsMatchExact,
		domain.TagsMatchAnyStrict, domain.TagsMatchAllStrict,
	}
	filters := [][]string{nil, {"a"}, {"  A ", "b"}, {"a", "b", "c"}}
	for _, mode := range modes {
		for _, ft := range filters {
			t.Run(fmt.Sprintf("%s/%q", mode, ft), func(t *testing.T) {
				t.Parallel()
				p := mustCompile(t, nil, domain.TagFilter{Tags: ft, Match: mode}, 0.45)
				checkLeaves(t, p)
				want := mode
				if want == domain.TagsMatchUnspecified {
					want = domain.TagsMatchAny
				}
				for _, u := range unitTagSets {
					if got, exp := p.Eval(u), domain.MatchTags(u, ft, want); got != exp {
						t.Errorf("unit %q: Eval = %v, MatchTags = %v (pred %+v)", u, got, exp, p)
					}
				}
			})
		}
	}

	t.Run("unspecified compiles to explicit any", func(t *testing.T) {
		t.Parallel()
		p := mustCompile(t, nil, domain.TagFilter{Tags: []string{"  Foo ", "bar", "foo"}}, 0.45)
		want := store.SQLPredicate{Op: store.PredicateAnd, Args: []store.SQLPredicate{
			{Op: store.PredicateTags, Tags: []string{"bar", "foo"}, Match: domain.TagsMatchAny},
		}}
		if !reflect.DeepEqual(p, want) {
			t.Fatalf("got %+v, want %+v", p, want)
		}
	})

	t.Run("exact with no tags selects only untagged", func(t *testing.T) {
		t.Parallel()
		p := mustCompile(t, nil, domain.TagFilter{Match: domain.TagsMatchExact}, 0.45)
		if len(p.Args) != 1 || !p.Eval(nil) || p.Eval([]string{"a"}) {
			t.Fatalf("exact empty filter compiled to %+v", p)
		}
	})
}

func gLeaf(match domain.TagsMatch, tags ...string) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: tags, Match: match}}
}

func gFuzzy(match domain.TagsMatch, tags ...string) domain.TagGroup {
	g := gLeaf(match, tags...)
	g.Leaf.Resolve = domain.TagResolveFuzzy
	return g
}

func gAllOf(gs ...domain.TagGroup) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupAllOf, Groups: gs}
}

func gAnyOf(gs ...domain.TagGroup) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupAnyOf, Groups: gs}
}

func gNoneOf(g domain.TagGroup) domain.TagGroup {
	return domain.TagGroup{Kind: domain.TagGroupNoneOf, Not: &g}
}

func TestCompileGroups(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		groups []domain.TagGroup
	}{
		{name: "leaf default any_strict", groups: []domain.TagGroup{gLeaf("", "a", "x")}},
		{name: "leaf any lenient", groups: []domain.TagGroup{gLeaf(domain.TagsMatchAny, "a")}},
		{name: "leaf all", groups: []domain.TagGroup{gLeaf(domain.TagsMatchAll, "a", "b")}},
		{name: "leaf all_strict", groups: []domain.TagGroup{gLeaf(domain.TagsMatchAllStrict, "A", "b")}},
		{name: "leaf exact", groups: []domain.TagGroup{gLeaf(domain.TagsMatchExact, "b", "a")}},
		{name: "leaf exact empty", groups: []domain.TagGroup{gLeaf(domain.TagsMatchExact)}},
		{name: "leaf empty", groups: []domain.TagGroup{gLeaf("")}},
		{name: "all_of", groups: []domain.TagGroup{gAllOf(gLeaf("", "a"), gLeaf("", "b"))}},
		{name: "any_of", groups: []domain.TagGroup{gAnyOf(gLeaf("", "c"), gLeaf(domain.TagsMatchExact, "x"))}},
		{name: "none_of", groups: []domain.TagGroup{gNoneOf(gLeaf("", "x"))}},
		{name: "none_of lenient", groups: []domain.TagGroup{gNoneOf(gLeaf(domain.TagsMatchAny, "x"))}},
		{name: "empty all_of", groups: []domain.TagGroup{gAllOf()}},
		{name: "empty any_of", groups: []domain.TagGroup{gAnyOf()}},
		{name: "nested", groups: []domain.TagGroup{
			gAnyOf(gAllOf(gLeaf("", "a"), gNoneOf(gLeaf("", "c"))), gLeaf(domain.TagsMatchAllStrict, "x", "y")),
		}},
		{name: "several top-level groups", groups: []domain.TagGroup{
			gLeaf(domain.TagsMatchAny, "a", "b"), gNoneOf(gLeaf("", "c")), gAnyOf(gLeaf("", "a"), gLeaf("", "x")),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &countingVocab{tags: []string{"a"}}
			p := mustCompile(t, v, domain.TagFilter{Groups: tc.groups}, 0.45)
			checkLeaves(t, p)
			if p.Op != store.PredicateAnd || len(p.Args) != len(tc.groups) {
				t.Fatalf("top level %+v, want And over %d groups", p, len(tc.groups))
			}
			if v.calls != 0 {
				t.Fatalf("vocabulary read %d times without fuzzy leaves", v.calls)
			}
			for _, u := range unitTagSets {
				exp := true
				for _, g := range tc.groups {
					exp = exp && domain.MatchGroup(u, g)
				}
				if got := p.Eval(u); got != exp {
					t.Errorf("unit %q: Eval = %v, MatchGroup = %v (pred %+v)", u, got, exp, p)
				}
			}
		})
	}

	t.Run("tags and groups", func(t *testing.T) {
		t.Parallel()
		f := domain.TagFilter{Tags: []string{"a"}, Groups: []domain.TagGroup{gLeaf("", "b")}}
		_, err := NewTagFilterCompiler(nil).Compile(context.Background(), tagsBank, f, 0.45)
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("err = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("malformed group", func(t *testing.T) {
		t.Parallel()
		f := domain.TagFilter{Groups: []domain.TagGroup{{Kind: domain.TagGroupNoneOf}}}
		_, err := NewTagFilterCompiler(nil).Compile(context.Background(), tagsBank, f, 0.45)
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("err = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("empty filter", func(t *testing.T) {
		t.Parallel()
		p := mustCompile(t, nil, domain.TagFilter{}, 0.45)
		if p.Op != store.PredicateAnd || len(p.Args) != 0 {
			t.Fatalf("empty filter compiled to %+v", p)
		}
		if !p.Eval(nil) || !p.Eval([]string{"a"}) {
			t.Fatal("empty filter must match untagged and tagged units")
		}
	})
}

var tagsVocab = stubVocab{
	"typescript", "python", "python3", "name:alice", "name:alicia", "scope:alice", "mongo",
}

func TestExpandToken(t *testing.T) {
	t.Parallel()
	vocab := normalizeOrdered(tagsVocab)
	cases := []struct {
		token  string
		minSim float64
		want   []string
	}{
		{token: "typscript", minSim: 0.45, want: []string{"typescript"}},
		{token: " Python ", minSim: 0.45, want: []string{"python"}}, // exact hit never widens to python3
		{token: "pythonn", minSim: 0.45, want: []string{"python", "python3"}},
		{token: "name:alicee", minSim: 0.45, want: []string{"name:alice"}},
		{token: "name:alicee", minSim: 0.3, want: []string{"name:alice", "name:alicia"}},
		{token: "name:alise", minSim: 0.32, want: []string{"name:alice"}}, // never scope:alice
		{token: "other:alice", minSim: 0.1, want: []string{"other:alice"}},
		{token: "zzzz", minSim: 0.45, want: []string{"zzzz"}},
	}
	for _, tc := range cases {
		if got := expandToken(tc.token, vocab, tc.minSim); !slices.Equal(got, tc.want) {
			t.Errorf("expandToken(%q, %v) = %q, want %q", tc.token, tc.minSim, got, tc.want)
		}
	}
}

func TestFuzzyResolution(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		group   domain.TagGroup
		minSim  float64
		match   [][]string
		noMatch [][]string
	}{
		{
			name: "typo resolves", group: gFuzzy(domain.TagsMatchAnyStrict, "Typscript"), minSim: 0.45,
			match: [][]string{{"typescript"}, {"typescript", "x"}}, noMatch: [][]string{nil, {"python"}},
		},
		{
			name: "default mode is any_strict", group: gFuzzy("", "typscript"), minSim: 0.45,
			match: [][]string{{"typescript"}}, noMatch: [][]string{nil},
		},
		{
			name: "exact hit wins", group: gFuzzy(domain.TagsMatchAnyStrict, "python"), minSim: 0.45,
			match: [][]string{{"python"}}, noMatch: [][]string{{"python3"}},
		},
		{
			name: "namespace isolation", group: gFuzzy(domain.TagsMatchAnyStrict, "name:alise"), minSim: 0.32,
			match: [][]string{{"name:alice"}}, noMatch: [][]string{{"scope:alice"}, {"name:alicia"}},
		},
		{
			name: "no match keeps token", group: gFuzzy(domain.TagsMatchAny, "zzzz"), minSim: 0.45,
			match: [][]string{nil, {"zzzz"}}, noMatch: [][]string{{"typescript"}},
		},
		{
			name: "any unions tokens", group: gFuzzy(domain.TagsMatchAnyStrict, "typscript", "mongo"), minSim: 0.45,
			match: [][]string{{"typescript"}, {"mongo"}}, noMatch: [][]string{nil, {"python"}},
		},
		{
			name: "all_strict is AND per token", group: gFuzzy(domain.TagsMatchAllStrict, "typscript", "pythonn"),
			minSim:  0.45,
			match:   [][]string{{"typescript", "python"}, {"typescript", "python3", "x"}},
			noMatch: [][]string{nil, {"typescript"}, {"python", "python3"}},
		},
		{
			name: "all keeps untagged", group: gFuzzy(domain.TagsMatchAll, "typscript", "pythonn"), minSim: 0.45,
			match: [][]string{nil, {"typescript", "python3"}}, noMatch: [][]string{{"typescript"}},
		},
		{
			name: "exact cross product", group: gFuzzy(domain.TagsMatchExact, "name:alicee", "typscript"),
			minSim: 0.3,
			match:  [][]string{{"name:alice", "typescript"}, {"typescript", "name:alicia"}},
			noMatch: [][]string{
				nil, {"name:alice"}, {"name:alice", "name:alicia", "typescript"}, {"name:alice", "typescript", "x"},
			},
		},
		{
			name: "exact degenerate keeps tokens", group: gFuzzy(domain.TagsMatchExact, "typscript", "typescrip"),
			minSim: 0.45,
			match:  [][]string{{"typscript", "typescrip"}}, noMatch: [][]string{nil, {"typescript"}},
		},
		{
			name: "fuzzy under none_of", group: gNoneOf(gFuzzy(domain.TagsMatchAnyStrict, "typscript")), minSim: 0.45,
			match: [][]string{nil, {"python"}}, noMatch: [][]string{{"typescript"}},
		},
		{
			name: "fuzzy leaf without tags", group: gFuzzy(domain.TagsMatchExact), minSim: 0.45,
			match: [][]string{nil}, noMatch: [][]string{{"typescript"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompile(t, tagsVocab, domain.TagFilter{Groups: []domain.TagGroup{tc.group}}, tc.minSim)
			checkLeaves(t, p)
			for _, u := range tc.match {
				if !p.Eval(u) {
					t.Errorf("unit %q should match %+v", u, p)
				}
			}
			for _, u := range tc.noMatch {
				if p.Eval(u) {
					t.Errorf("unit %q should not match %+v", u, p)
				}
			}
		})
	}
	t.Run("errors", testFuzzyErrors)
}

// testFuzzyErrors covers a missing or failing vocabulary, the single vocabulary read and the exact-mode
// combination ceiling.
func testFuzzyErrors(t *testing.T) {
	t.Parallel()
	run := func(v TagVocabulary, f domain.TagFilter) error {
		if _, err := NewTagFilterCompiler(v).Compile(context.Background(), tagsBank, f, 0.45); err != nil {
			return fmt.Errorf("compile: %w", err)
		}
		return nil
	}
	f := domain.TagFilter{Groups: []domain.TagGroup{gAllOf(gLeaf("", "a"), gFuzzy("", "typscript"))}}

	if err := run(nil, f); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Errorf("nil vocabulary: err = %v, want ErrFailedPrecondition", err)
	}

	boom := errors.New("boom")
	v := &countingVocab{err: boom}
	if err := run(v, f); !errors.Is(err, boom) {
		t.Errorf("vocabulary error: err = %v, want wrapped boom", err)
	}

	v = &countingVocab{tags: tagsVocab}
	f.Groups = append(f.Groups, gFuzzy(domain.TagsMatchAny, "pythonn"))
	if err := run(v, f); err != nil || v.calls != 1 {
		t.Errorf("two fuzzy leaves: err = %v, vocabulary reads = %d, want nil and 1", err, v.calls)
	}

	var many stubVocab
	for i := range 6 {
		many = append(many, fmt.Sprintf("x:projectalpha%d", i), fmt.Sprintf("y:projectalpha%d", i))
	}
	tooMany := domain.TagFilter{Groups: []domain.TagGroup{
		gFuzzy(domain.TagsMatchExact, "x:projectalpha", "y:projectalpha"),
	}}
	if err := run(many, tooMany); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("36 combinations: err = %v, want ErrInvalidArgument", err)
	}
	tooMany.Groups[0].Leaf.Match = domain.TagsMatchAnyStrict
	if err := run(many, tooMany); err != nil {
		t.Errorf("any_strict has no ceiling: err = %v", err)
	}
}
