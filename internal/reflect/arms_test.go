package reflect_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/recall"
	rf "memd/internal/reflect"
	"sync/atomic"
	"testing"
)

// probeArm is a recall arm that counts its calls and returns unit u1.
type probeArm struct {
	name  string
	calls atomic.Int32
}

func (a *probeArm) Name() string { return a.name }

func (a *probeArm) Description() string { return "probe" }

func (a *probeArm) Retrieve(context.Context, recall.Query) ([]recall.Hit, error) {
	a.calls.Add(1)
	return []recall.Hit{{ID: uid("u1"), Score: 1}}, nil
}

// TestReflectArmsReachRecallTool: the request's arms select the arms the recall tool runs; an unknown arm fails
// the call with INVALID_ARGUMENT before the loop starts.
func TestReflectArmsReachRecallTool(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	first, second := &probeArm{name: "first"}, &probeArm{name: "second"}
	h.arms = []recall.Arm{first, second}
	h.build()
	h.turns(recallCall(), doneCall("tea", "u1"))
	res, err := h.reflect(func(r *rf.Request) { r.Arms = []string{"second"} })
	if err != nil {
		t.Fatal(err)
	}
	if first.calls.Load() != 0 || second.calls.Load() != 1 || len(res.BasedOn.Memories) != 1 {
		t.Fatalf("first called %d, second %d, cited %d; want only second, citing u1", first.calls.Load(),
			second.calls.Load(), len(res.BasedOn.Memories))
	}
	turns := len(h.loopCalls())
	_, err = h.reflect(func(r *rf.Request) { r.Arms = []string{"vector"} })
	if !errors.Is(err, domain.ErrInvalidArgument) || len(h.loopCalls()) != turns {
		t.Fatalf("unknown arm: err = %v after %d more turns; want INVALID_ARGUMENT before the loop", err,
			len(h.loopCalls())-turns)
	}
}
