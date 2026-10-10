package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock advances only when slept on, so polling and latency are deterministic.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.advance(d)
	return nil
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeTarget scripts the pending-operation polls and answers every question with the stored content.
type fakeTarget struct {
	clock   *fakeClock
	mu      sync.Mutex
	polls   []map[string]int64 // returned in order; then empty
	pollN   int
	content map[string]string
	failOn  string // session id whose retain fails
}

var _ Target = (*fakeTarget)(nil)

func (f *fakeTarget) Name() string { return "fake" }

func (f *fakeTarget) Retain(_ context.Context, bank string, s Session) ([]Usage, error) {
	f.clock.advance(10 * time.Millisecond)
	if s.ID == f.failOn {
		return nil, errors.New("boom")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.content == nil {
		f.content = map[string]string{}
	}
	f.content[bank] += s.Content + "\n"
	return []Usage{{Slot: "EXTRACT", InputTokens: 7, OutputTokens: 3}}, nil
}

func (f *fakeTarget) PendingOperations(context.Context, string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollN++
	if len(f.polls) == 0 {
		return map[string]int64{}, nil
	}
	p := f.polls[0]
	f.polls = f.polls[1:]
	return p, nil
}

func (f *fakeTarget) Recall(_ context.Context, bank, _ string, _ time.Time) ([]string, error) {
	f.clock.advance(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Split(strings.TrimSpace(f.content[bank]), "\n"), nil
}

func (f *fakeTarget) Reflect(_ context.Context, bank, _ string, _ time.Time) (string, []Usage, error) {
	f.clock.advance(30 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content[bank], []Usage{{Slot: "REFLECT", InputTokens: 100, OutputTokens: 10}}, nil
}

func (f *fakeTarget) Background(context.Context, string) (BackgroundStats, error) {
	return BackgroundStats{Usage: []Usage{{Slot: "CONSOLIDATE", InputTokens: 5, OutputTokens: 1}}, Failed: 1}, nil
}

// containsJudge marks a response correct when it contains the gold answer.
type containsJudge struct{}

func (containsJudge) Grade(_ context.Context, q Question, response string, _ bool) (Verdict, error) {
	return Verdict{
		Correct: strings.Contains(response, q.Answer),
		Usage:   []Usage{{Slot: "JUDGE", InputTokens: 1, OutputTokens: 1}},
	}, nil
}

func testConversations() []Conversation {
	return []Conversation{
		{
			ID:       "c1",
			Sessions: []Session{{ID: "s1", Content: "Alice lives in Paris"}, {ID: "s2", Content: "Bob owns a cat"}},
			Questions: []Question{
				{ID: "q1", Category: "single", Query: "Where does Alice live?", Answer: "Paris"},
				{ID: "q2", Category: "multi", Query: "What pet?", Answer: "dog"},
			},
		},
		{
			ID:        "c/2",
			Sessions:  []Session{{ID: "bad", Content: "x"}},
			Questions: []Question{{ID: "q3", Category: "single", Query: "?", Answer: "x"}},
		},
	}
}

func TestRunReportsAccuracyUsageAndLatency(t *testing.T) {
	for _, mode := range []string{ModeReflect, ModeRecall} {
		t.Run(mode, func(t *testing.T) {
			clock := &fakeClock{}
			target := &fakeTarget{clock: clock, failOn: "bad"}
			r := NewRunner(target, containsJudge{}, clock, Config{Mode: mode, BankPrefix: "run-"})
			rep, err := r.Run(context.Background(), testConversations())
			if err != nil {
				t.Fatal(err)
			}
			if rep.Overall.Total != 3 || rep.Overall.Correct != 1 || rep.Overall.Errors != 1 {
				t.Fatalf("overall = %+v", rep.Overall)
			}
			if len(rep.Categories) != 2 || rep.Categories[0].Category != "multi" {
				t.Fatalf("categories = %+v", rep.Categories)
			}
			if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "boom") {
				t.Fatalf("errors = %v", rep.Errors)
			}
			if rep.FailedOperations != 1 {
				t.Fatalf("failed ops = %d", rep.FailedOperations)
			}
			checkUsageAndLatency(t, rep, mode)
		})
	}
}

func checkUsageAndLatency(t *testing.T, rep TargetReport, mode string) {
	t.Helper()
	slots := map[string]Usage{}
	for _, u := range rep.Usage {
		slots[u.Slot] = u
	}
	if slots["EXTRACT"].InputTokens != 14 || slots["JUDGE"].InputTokens != 2 || slots["CONSOLIDATE"].InputTokens != 5 {
		t.Fatalf("usage = %+v", rep.Usage)
	}
	if _, ok := slots["REFLECT"]; ok != (mode == ModeReflect) {
		t.Fatalf("REFLECT usage present = %v in mode %s", ok, mode)
	}
	ops := map[string]LatencyStats{}
	for _, l := range rep.Latency {
		ops[l.Op] = l
	}
	for _, op := range []string{"retain", "barrier", mode, "judge"} {
		if ops[op].Count == 0 {
			t.Fatalf("no latency for %s: %+v", op, rep.Latency)
		}
	}
	if ops["retain"].P50Ms != 10 {
		t.Fatalf("retain p50 = %v", ops["retain"].P50Ms)
	}
}

func TestWaitIdleNeedsQuietPolls(t *testing.T) {
	clock := &fakeClock{}
	target := &fakeTarget{clock: clock, polls: []map[string]int64{
		{"consolidate": 1}, {}, {"consolidate": 1}, {}, {},
	}}
	r := NewRunner(target, containsJudge{}, clock, Config{PollInterval: time.Second, QuietPolls: 2})
	if err := r.waitIdle(context.Background(), "b", newRecorder(0)); err != nil {
		t.Fatal(err)
	}
	if target.pollN != 5 {
		t.Fatalf("polls = %d, want 5 (a single quiet poll between busy ones must not end the barrier)", target.pollN)
	}
}

func TestWaitIdleTimeout(t *testing.T) {
	clock := &fakeClock{}
	busy := make([]map[string]int64, 100)
	for i := range busy {
		busy[i] = map[string]int64{"consolidate": 1}
	}
	target := &fakeTarget{clock: clock, polls: busy}
	r := NewRunner(target, containsJudge{}, clock, Config{PollInterval: time.Second, SettleTimeout: 5 * time.Second})
	err := r.waitIdle(context.Background(), "b", newRecorder(0))
	if err == nil || !strings.Contains(err.Error(), "consolidate") {
		t.Fatalf("err = %v, want a timeout naming the pending kind", err)
	}
}

func TestPercentiles(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	tests := []struct {
		p    float64
		in   []time.Duration
		want time.Duration
	}{
		{50, ds, 50 * time.Millisecond},
		{95, ds, 95 * time.Millisecond},
		{95, ds[:1], time.Millisecond},
		{50, nil, 0},
	}
	for _, tt := range tests {
		if got := Percentile(tt.in, tt.p); got != tt.want {
			t.Errorf("Percentile(n=%d, %v) = %v, want %v", len(tt.in), tt.p, got, tt.want)
		}
	}
}

func TestReportSections(t *testing.T) {
	clock := &fakeClock{}
	r := NewRunner(&fakeTarget{clock: clock, failOn: "bad"}, containsJudge{}, clock, Config{})
	tr, err := r.Run(context.Background(), testConversations())
	if err != nil {
		t.Fatal(err)
	}
	other := tr
	other.Target = "hindsight"
	rep := Report{Dataset: "unit", Mode: ModeReflect, Targets: []TargetReport{tr, other}}

	var js bytes.Buffer
	if err := rep.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	target := decoded["targets"].([]any)[0].(map[string]any)
	for _, key := range []string{"overall", "categories", "usage", "latency", "questions"} {
		if _, ok := target[key]; !ok {
			t.Errorf("JSON report lacks %q", key)
		}
	}
	lat := target["latency"].([]any)[0].(map[string]any)
	for _, key := range []string{"p50_ms", "p95_ms"} {
		if _, ok := lat[key]; !ok {
			t.Errorf("latency lacks %q", key)
		}
	}

	var md bytes.Buffer
	if err := rep.WriteMarkdown(&md); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Accuracy by category", "| Category | fake | hindsight |", "| single | 50.0% (1/2), 1 errors |",
		"## Token usage per slot", "| JUDGE | 2 / 2 |",
		"## Latency (ms)", "fake p50 | fake p95", "| reflect | 30.0 | 30.0 | 2 |",
		"## Problems", "c/2: retain session bad: boom",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md.String())
		}
	}
}

func TestBankID(t *testing.T) {
	if got := BankID("eval-1-", "conv 26/ä"); got != "eval-1-conv-26--" {
		t.Fatalf("BankID = %q", got)
	}
}

func TestFilter(t *testing.T) {
	convs := testConversations()
	got := Filter{Categories: []string{"multi"}}.Apply(convs)
	if len(got) != 1 || len(got[0].Questions) != 1 || got[0].Questions[0].ID != "q2" {
		t.Fatalf("category filter = %+v", got)
	}
	got = Filter{MaxConversations: 1, MaxQuestions: 1}.Apply(convs)
	if len(got) != 1 || len(got[0].Questions) != 1 {
		t.Fatalf("limit filter = %+v", got)
	}
	if len(convs[0].Questions) != 2 {
		t.Fatal("Apply modified its input")
	}
}
