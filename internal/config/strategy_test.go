package config

import (
	"memd/internal/domain"
	"testing"
)

// TestMatchStrategy: the first strategy with a matching pattern wins; unspecified tags_match is all_strict
// (containment, never the shared scope); exact needs the same set; an empty pattern only matches the shared scope
// under exact.
func TestMatchStrategy(t *testing.T) {
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
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	for _, tc := range tests {
		st, ok := MatchStrategy(strategies, domain.NewScopeKey(b, tc.tags))
		got := ""
		if ok {
			got = st.Name
		}
		if got != tc.want {
			t.Errorf("MatchStrategy(%q) = %q, want %q", tc.tags, got, tc.want)
		}
	}
}
