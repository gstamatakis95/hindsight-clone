package mentalmodel

import (
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// TestSynthesizeDirectivesMatchReflect pins the one directive rule across the strategies: the single-shot synthesize
// prompt applies exactly the directives reflect applies to the request the agentic strategy sends it (the model's
// scope as its tag filter), both through domain.SelectDirectives. For a model tagged [team ops] (all_strict) a
// directive tagged only [team] no longer applies: the scope must hold every tag of the request, not just share one.
func TestSynthesizeDirectivesMatchReflect(t *testing.T) {
	e := newEnv(t)
	var active []domain.Directive
	e.tx(func(tx store.Tx) error {
		for _, d := range []domain.Directive{
			{Name: "global", Rules: []string{"g"}, Active: true},
			{Name: "team", Rules: []string{"t"}, Tags: []string{"team"}, Active: true},
			{Name: "team-ops", Rules: []string{"to"}, Tags: []string{"ops", "team"}, Active: true},
			{Name: "sales", Rules: []string{"s"}, Tags: []string{"sales"}, Active: true},
			{Name: "off", Rules: []string{"o"}, Tags: []string{"team"}},
		} {
			d.Bank = e.bank
			if _, err := tx.Directives().Upsert(e.ctx, d); err != nil {
				return err
			}
		}
		var err error
		active, err = tx.Directives().List(e.ctx, e.bank, true)
		return err
	})
	agentic := NewAgenticSynthesizer(e.rf, ModelDeps{LLM: e.llm, Router: ai.NewRouter(), Clock: e.clock})
	for _, tc := range []struct {
		id   domain.MentalModelID
		tags []string
		want []string
	}{
		{"untagged", nil, []string{"global"}},
		{"team", []string{"team"}, []string{"global", "team", "team-ops"}},
		{"team-ops", []string{"ops", "team"}, []string{"global", "team-ops"}},
	} {
		m := e.addModel(domain.MentalModelSpec{ID: tc.id, Name: string(tc.id), SourceQuery: "q", Tags: tc.tags})
		synthesize, err := scopeDirectives(e.ctx, e.store, m)
		if err != nil {
			t.Fatal(err)
		}
		if got := directiveNames(synthesize); !slices.Equal(got, tc.want) {
			t.Errorf("%s: synthesize directives = %v, want %v", tc.id, got, tc.want)
		}
		before := len(e.rf.reqs)
		_, _ = agentic.Full(e.ctx, m, domain.BankConfig{}, 1) // the scope is empty; only the request matters
		if len(e.rf.reqs) != before+1 {
			t.Fatalf("%s: the agentic strategy made %d reflect calls, want 1", tc.id, len(e.rf.reqs)-before)
		}
		req := e.rf.reqs[before]
		if got := directiveNames(domain.SelectDirectives(active, req.Tags, req.ApplyAllDirectives)); !slices.Equal(got,
			tc.want) {
			t.Errorf("%s: reflect (agentic) directives = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// directiveNames returns the names of ds, sorted.
func directiveNames(ds []domain.Directive) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	slices.Sort(out)
	return out
}
