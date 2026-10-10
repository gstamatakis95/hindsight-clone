package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackgroundCollectedAfterBarrierFailure(t *testing.T) {
	clock := &fakeClock{}
	busy := make([]map[string]int64, 100)
	for i := range busy {
		busy[i] = map[string]int64{"consolidate": 1}
	}
	target := &fakeTarget{clock: clock, polls: busy}
	r := NewRunner(target, containsJudge{}, clock, Config{PollInterval: time.Second, SettleTimeout: 3 * time.Second})
	rep, err := r.Run(context.Background(), testConversations()[:1])
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overall.Errors != 2 || rep.FailedOperations != 1 {
		t.Fatalf("overall = %+v, failed ops = %d; want both questions errored and background still read",
			rep.Overall, rep.FailedOperations)
	}
}

func TestRunRejectsBankIDCollision(t *testing.T) {
	clock := &fakeClock{}
	convs := []Conversation{{ID: "a/b"}, {ID: "a-b"}}
	_, err := NewRunner(&fakeTarget{clock: clock}, containsJudge{}, clock, Config{}).Run(context.Background(), convs)
	if err == nil || !strings.Contains(err.Error(), "share bank id") {
		t.Fatalf("err = %v, want a bank id collision", err)
	}
}

// naJudge marks every question not applicable.
type naJudge struct{}

func (naJudge) Grade(context.Context, Question, string, bool) (Verdict, error) {
	return Verdict{NotApplicable: true, Reason: "n/a"}, nil
}

func TestNotApplicableLeftOutOfAccuracy(t *testing.T) {
	clock := &fakeClock{}
	rep, err := NewRunner(&fakeTarget{clock: clock}, naJudge{}, clock, Config{}).
		Run(context.Background(), testConversations()[:1])
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overall.Total != 0 || rep.Overall.NotApplicable != 2 || rep.Overall.Correct != 0 {
		t.Fatalf("overall = %+v, want 0 graded and 2 not applicable", rep.Overall)
	}
}

// slowTarget blocks Recall until its context ends.
type slowTarget struct{ *fakeTarget }

func (slowTarget) Recall(ctx context.Context, _, _ string, _ time.Time) ([]string, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("recall: %w", ctx.Err())
}

func TestCallTimeoutBoundsEachCall(t *testing.T) {
	clock := &fakeClock{}
	target := slowTarget{&fakeTarget{clock: clock}}
	r := NewRunner(target, containsJudge{}, clock, Config{Mode: ModeRecall, CallTimeout: 20 * time.Millisecond})
	rep, err := r.Run(context.Background(), testConversations()[:1])
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overall.Errors != 2 || !strings.Contains(rep.Questions[0].Error, "deadline exceeded") {
		t.Fatalf("questions = %+v, want per-call deadline errors", rep.Questions)
	}
}
