package recall

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// TestRecallExtraArm: an extra arm registered beside the built-ins is selected by recall.arms, boosted by
// arm_boosts, traced and scored by name, and the engine re-check drops every hit the query excludes: another
// bank's unit, a filtered-out fact type and a tag-filtered unit.
func TestRecallExtraArm(t *testing.T) {
	vec, grp := arm(armVector, "a", "b"), arm(armGraph, "a")
	fresh := arm("fresh", "c", "d")
	e := newEnv(t, envOpts{arms: []Arm{vec, grp, fresh}})
	e.add(t, fact("a", "x a"), fact("b", "x b"), fact("c", "x c"), fact("d", "x d"))
	if got := e.rc.Arms(); len(got) != 3 || got[2] != (ArmInfo{Name: "fresh", Description: "stub fresh"}) {
		t.Fatalf("Arms() = %+v", got)
	}

	// Enabled by config: graph is registered but not selected. With a pool cap of 2, the unboosted cut keeps a
	// and c (ties keep registration order); boosting fresh x10 keeps c and d instead.
	cfg := domain.RecallConfig{Arms: []string{armVector, "fresh"}, RerankerMaxCandidates: ptr(2)}
	res := e.recall(t, Request{Query: "x", Overrides: recallCfg(cfg), Trace: true})
	if got := sortedIDs(res); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("unboosted = %v, want [a c]", got)
	}
	if grp.called != 0 || len(res.Trace.Arms) != 2 || res.Trace.Arms[1].Arm != "fresh" {
		t.Fatalf("graph called %d times, trace %+v; want vector and fresh only", grp.called, res.Trace.Arms)
	}
	cfg.ArmBoosts = map[string]float64{"fresh": 10}
	res = e.recall(t, Request{Query: "x", Overrides: recallCfg(cfg)})
	if got := sortedIDs(res); !slices.Equal(got, []string{"c", "d"}) {
		t.Fatalf("fresh boosted = %v, want [c d]", got)
	}
	if s := res.Results[0].Scores.Arms; len(s) != 1 || s["fresh"] == 0 {
		t.Fatalf("scores = %+v, want fresh's raw score", s)
	}

	// The re-check: fresh leaks another bank's unit, an observation and a tagged unit.
	other := domain.BankRef{Tenant: testBank.Tenant, Bank: "other"}
	err := e.store.InTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if _, err := tx.Banks().Ensure(ctx, other); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, other)
		if err != nil {
			return err
		}
		x := fact("x", "x elsewhere")
		x.Bank, x.Seq, x.Tags = other, seq, []string{}
		return tx.Units().Insert(ctx, []domain.Unit{x})
	})
	if err != nil {
		t.Fatal(err)
	}
	e.add(t, fact("secret", "x secret", "secret"),
		domain.Unit{ID: uid("obs"), Bank: testBank, Type: domain.FactTypeObservation, Text: "x obs"})
	fresh.hits = arm("fresh", "x", "obs", "secret", "c").hits
	res = e.recall(t, Request{
		Query: "x", Types: []domain.FactType{domain.FactTypeWorld},
		Tags:      domain.TagFilter{Groups: []domain.TagGroup{gNoneOf(gLeaf(domain.TagsMatchAnyStrict, "secret"))}},
		Overrides: recallCfg(domain.RecallConfig{Arms: []string{armVector, "fresh"}}), Trace: true,
	})
	if got := sortedIDs(res); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("re-checked = %v, want [a b c]", got)
	}
	if n := res.Trace.Arms[1].Candidates; n != 4 {
		t.Fatalf("fresh traced %d candidates, want the 4 it returned", n)
	}
}

// TestRecallArmSelection: the request's arms win over recall.arms and run in registration order; unknown or
// repeated names (arms and min_scores.arms) are INVALID_ARGUMENT, as is registering a name twice.
func TestRecallArmSelection(t *testing.T) {
	vec, txt, grp := arm(armVector, "a"), arm(armText, "a"), arm(armGraph, "a")
	e := newEnv(t, envOpts{arms: []Arm{vec, txt, grp}})
	e.add(t, fact("a", "x a"))
	only := recallCfg(domain.RecallConfig{Arms: []string{armText}})
	res := e.recall(t, Request{Query: "x", Arms: []string{armGraph, armVector}, Overrides: only, Trace: true})
	var ran []string
	for _, a := range res.Trace.Arms {
		ran = append(ran, a.Arm)
	}
	if !slices.Equal(ran, []string{armVector, armGraph}) || txt.called != 0 {
		t.Fatalf("ran %v (text called %d times), want [vector graph]", ran, txt.called)
	}
	for _, req := range []Request{
		{Arms: []string{"nope"}},
		{Arms: []string{armVector, armVector}},
		{MinScores: domain.MinScores{Arms: map[string]float64{"nope": 0.5}}},
	} {
		req.Bank, req.Query = testBank, "x"
		if _, err := e.rc.Recall(context.Background(), req); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%+v: err = %v, want INVALID_ARGUMENT", req, err)
		}
	}
	if err := e.rc.CheckArms([]string{armText, "nope"}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("CheckArms = %v, want INVALID_ARGUMENT", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("New with a repeated arm name did not panic")
		}
	}()
	New(Deps{DB: e.store, Arms: []Arm{vec, arm(armVector)}})
}
