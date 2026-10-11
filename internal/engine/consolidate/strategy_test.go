package consolidate

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// The StrategyMatcher is what config.NewResolver consumes.
var _ config.StrategyMatcher = NewStrategyMatcher(nil)

// TestStrategyMatch: the first strategy with a matching pattern wins; unspecified tags_match is
// all_strict (containment, never the shared scope); exact needs the same set; an empty pattern only
// matches the shared scope under exact.
func TestStrategyMatch(t *testing.T) {
	strategies := []domain.ConsolidationStrategy{
		{Name: "exact-acme", Scopes: []domain.ScopePattern{
			{Tags: []string{"company:acme"}, TagsMatch: domain.TagsMatchExact},
		}},
		{Name: "team", Scopes: []domain.ScopePattern{{Tags: []string{"team"}}, {Tags: []string{"squad"}}}},
		{Name: "any-acme", Scopes: []domain.ScopePattern{{Tags: []string{"company:acme"}}}},
		{Name: "empty", Scopes: []domain.ScopePattern{{}}},
		{Name: "shared", Scopes: []domain.ScopePattern{{TagsMatch: domain.TagsMatchExact}}},
	}
	tests := []struct {
		tags []string
		want string
	}{
		{[]string{"company:acme"}, "exact-acme"},
		{[]string{"company:acme", "team"}, "team"},
		{[]string{"squad", "x"}, "team"},
		{[]string{"company:acme", "user:dana"}, "any-acme"},
		{nil, "shared"},
		{[]string{"other"}, ""},
	}
	m := NewStrategyMatcher(nil)
	for _, tc := range tests {
		st, ok := m.Match(strategies, scopeOf(tc.tags...))
		got := ""
		if ok {
			got = st.Name
		}
		if got != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.tags, got, tc.want)
		}
	}
}

// manyScopes is a test-only ScopeLister reporting more scopes than Preview examines.
type manyScopes struct{}

// ListScopes returns limit scopes when asked for previewScopeLimit+1 of them.
func (manyScopes) ListScopes(_ context.Context, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error) {
	if after != "" || limit != previewScopeLimit+1 {
		return nil, errors.New("unexpected paging")
	}
	return make([]domain.ScopeInfo, limit), nil
}

// TestStrategyPreviewIncomplete: more scopes than the preview limit make Complete false.
func TestStrategyPreviewIncomplete(t *testing.T) {
	p, err := NewStrategyMatcher(manyScopes{}).Preview(t.Context(), bank, nil)
	if err != nil || p.Complete {
		t.Fatalf("preview = %+v, %v; want incomplete", p.Complete, err)
	}
}

// TestStrategyPreview: each draft strategy lists the scopes it wins and those an earlier one shadows, over
// the scopes the fake's Reader.ListScopes returns (every scope that holds an observation).
func TestStrategyPreview(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	for i, tags := range [][]string{{"team", "x"}, {"team"}, {"other"}, {}} {
		id := fmt.Sprintf("f%d", i)
		e.fact(id, "fact "+id, tags...)
		e.observation("obs-"+id, "obs "+id, tags, id)
	}
	m := NewStrategyMatcher(e.store.Read())
	draft := []domain.ConsolidationStrategy{
		{Name: "exact-team", Scopes: []domain.ScopePattern{{Tags: []string{"team"}, TagsMatch: domain.TagsMatchExact}}},
		{Name: "team", Scopes: []domain.ScopePattern{{Tags: []string{"team"}}}},
	}
	p, err := m.Preview(t.Context(), bank, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Complete || len(p.Matches) != 2 {
		t.Fatalf("preview = %+v", p)
	}
	keys := func(ss []port.ScopeKey) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.String())
		}
		return out
	}
	if got := keys(p.Matches[0].MatchedScopes); len(got) != 1 || got[0] != scopeOf("team").String() ||
		len(p.Matches[0].ShadowedScopes) != 0 || p.Matches[0].Strategy != "exact-team" {
		t.Errorf("exact-team = %+v", p.Matches[0])
	}
	if got := keys(p.Matches[1].MatchedScopes); len(got) != 1 || got[0] != scopeOf("team", "x").String() {
		t.Errorf("team matched = %v", got)
	}
	if got := keys(p.Matches[1].ShadowedScopes); len(got) != 1 || got[0] != scopeOf("team").String() {
		t.Errorf("team shadowed = %v", got)
	}
	if _, err := NewStrategyMatcher(nil).Preview(t.Context(), bank, draft); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("preview without a lister: err = %v", err)
	}
}
