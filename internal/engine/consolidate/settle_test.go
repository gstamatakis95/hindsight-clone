package consolidate

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// hookLLM decorates an LLM: fn sees every Structured request first and may fail it.
type hookLLM struct {
	port.LLM
	fn func(req port.StructuredRequest) error
}

// Structured runs the hook, then the decorated call.
func (h hookLLM) Structured(ctx context.Context, req port.StructuredRequest, out any) (port.Usage, error) {
	if err := h.fn(req); err != nil {
		return port.Usage{}, fmt.Errorf("%w: hook: %w", domain.ErrUnavailable, err)
	}
	return h.LLM.Structured(ctx, req, out)
}

// TestApplyTakesSeqFirst: every apply transaction takes tx.Seq before re-reading facts, even one that only
// stamps (two empty decisions bump the bank seq twice, plus once for the final transaction).
func TestApplyTakesSeqFirst(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Kim sails", "a")
	e.fact("f2", "Lee rows", "b")
	before, err := e.store.Read().BankSeq(t.Context(), bank)
	if err != nil {
		t.Fatal(err)
	}
	e.script(ok(reply(nil, nil)))
	e.run(autoJob())
	after, err := e.store.Read().BankSeq(t.Context(), bank)
	if err != nil {
		t.Fatal(err)
	}
	if after-before != 3 {
		t.Fatalf("bank seq moved by %d, want 3 (two stamp-only applies and the final tx)", after-before)
	}
	if e.unit("f1").ConsolidatedAt == nil || e.unit("f2").ConsolidatedAt == nil {
		t.Fatal("want both stamped")
	}
}

// TestSettleProbesTheScopeBinding: a strategy routes scope x to a broken model while the bank's default is
// healthy. The probe uses the broken binding, fails, and nothing is parked.
func TestSettleProbesTheScopeBinding(t *testing.T) {
	e := newEnv(t, domain.BankConfig{
		ConsolidationStrategies: []domain.ConsolidationStrategy{{
			Name: "s", Scopes: []domain.ScopePattern{{Tags: []string{"x"}}}, Models: consolidateModel("broken"),
		}},
	})
	e.fact("f1", "Fact one", "x")
	e.script(ok(reply(nil, nil)))
	var models []string
	e.deps.LLM = hookLLM{LLM: e.llm, fn: func(req port.StructuredRequest) error {
		models = append(models, req.Binding.Model)
		if req.Binding.Model == "broken" {
			return errors.New("model down")
		}
		return nil
	}}
	if _, err := e.consolidator().Run(t.Context(), autoJob()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if want := []string{"broken", "broken"}; fmt.Sprint(models) != fmt.Sprint(want) {
		t.Fatalf("models called = %v, want %v (the batch, then the probe on the same binding)", models, want)
	}
	if e.unit("f1").ConsolidationFailedAt != nil {
		t.Fatal("parked on another binding's health")
	}
}

// TestSettleRetriesAloneBeforeParking: the whole batch fails, then the gateway recovers at the probe. Each
// deferred unit is retried alone, succeeds, and none of the 4 is parked.
func TestSettleRetriesAloneBeforeParking(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationLLMBatchSize: ptr(4)})
	for _, id := range []string{"f1", "f2", "f3", "f4"} {
		e.fact(id, "Fact "+id, "x")
	}
	e.script(fail(), fail(), fail(), fail(), fail(), fail(), fail(), ok(reply(nil, nil)))
	res := e.run(autoJob())
	for _, id := range []string{"f1", "f2", "f3", "f4"} {
		if u := e.unit(id); u.ConsolidationFailedAt != nil || u.ConsolidatedAt == nil {
			t.Errorf("%s: parked %v, stamped %v; want stamped, not parked", id, u.ConsolidationFailedAt != nil,
				u.ConsolidatedAt != nil)
		}
	}
	if res.Processed != 4 {
		t.Fatalf("result %+v", res)
	}
}

// TestParkSkipsChangedUnits: a unit edited between its failed retry and the park is not parked; it is
// left for the next run.
func TestParkSkipsChangedUnits(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("p", "Poison fact", "x")
	e.script(fail(), ok(reply(nil, nil)), fail())
	calls := 0
	e.deps.LLM = hookLLM{LLM: e.llm, fn: func(port.StructuredRequest) error {
		if calls++; calls == 3 { // the retry alone: edit p while it runs
			e.tx(func(tx port.Tx) error {
				u := e.unit("p")
				seq, err := tx.Seq(t.Context(), bank)
				if err != nil {
					return err
				}
				u.Text, u.Seq = "Fixed fact", seq
				return tx.Units().Update(t.Context(), u)
			})
		}
		return nil
	}}
	e.run(autoJob())
	if e.unit("p").ConsolidationFailedAt != nil {
		t.Fatal("an edited unit was parked")
	}
}

// TestCircuitBreakerAfterTwoDeadBatches: two top-level batches in a row in which every call fails, with no
// success between them, fail the run instead of parking the second batch's units.
func TestCircuitBreakerAfterTwoDeadBatches(t *testing.T) {
	e := newEnv(t, domain.BankConfig{ConsolidationLLMBatchSize: ptr(1)})
	e.fact("f1", "One", "x")
	e.fact("f2", "Two", "x")
	e.script(fail(), ok(reply(nil, nil)), fail(), fail())
	if _, err := e.consolidator().Run(t.Context(), autoJob()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	e.wantBatches([]string{"f1"}, []string{}, []string{"f1"}, []string{"f2"})
	if e.unit("f1").ConsolidationFailedAt == nil {
		t.Fatal("f1 (failed twice with a healthy probe between) not parked")
	}
	if e.unit("f2").ConsolidationFailedAt != nil {
		t.Fatal("f2 parked although the breaker tripped")
	}
}
