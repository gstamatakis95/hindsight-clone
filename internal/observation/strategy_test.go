package observation

import (
	"fmt"
	"memd/internal/domain"
	"testing"
)

// TestStrategyPreview: each draft strategy lists the scopes it wins and those an earlier one shadows, over
// the scopes Reader.ListObservationScopes returns (every scope that holds an observation).
func TestStrategyPreview(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	for i, tags := range [][]string{{"team", "x"}, {"team"}, {"other"}, {}} {
		id := fmt.Sprintf("f%d", i)
		e.fact(id, "fact "+id, tags...)
		e.observation("obs-"+id, "obs "+id, tags, id)
	}
	draft := []domain.ConsolidationStrategy{
		{Name: "exact-team", Scopes: []domain.TagFilter{{Tags: []string{"team"}, Match: domain.TagsMatchExact}}},
		{Name: "team", Scopes: []domain.TagFilter{{Tags: []string{"team"}}}},
	}
	p, err := e.service().PreviewConsolidationStrategies(t.Context(), bank, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Complete || len(p.Matches) != 2 {
		t.Fatalf("preview = %+v", p)
	}
	keys := func(ss []domain.ScopeKey) []string {
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
}
