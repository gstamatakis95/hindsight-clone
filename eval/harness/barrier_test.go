package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock advances only when slept on, so polling is deterministic.
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
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

// pollTarget scripts PendingOperations: busy for the listed polls (true = busy), idle afterwards unless
// alwaysBusy.
type pollTarget struct {
	Target
	busy       []bool
	alwaysBusy bool
	polls      int
}

func (p *pollTarget) Name() string { return "fake" }

func (p *pollTarget) PendingOperations(context.Context, string) (map[string]int64, error) {
	p.polls++
	if p.alwaysBusy || (p.polls <= len(p.busy) && p.busy[p.polls-1]) {
		return map[string]int64{"consolidation": 1}, nil
	}
	return map[string]int64{}, nil
}

func newBarrierRunner(t *testing.T, tgt Target) *Runner {
	t.Helper()
	j, err := OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	r, err := NewRunner(Deps{Target: tgt, Judge: noJudge{}, Journal: j, Clock: &fakeClock{}},
		Config{PollInterval: time.Second, QuietPolls: 2, SettleTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type noJudge struct{}

func (noJudge) Grade(context.Context, Question, string, bool) (Verdict, error) { return Verdict{}, nil }

// TestBarrierNeedsQuietPolls checks that one idle poll between two busy ones does not end the wait: a
// job can finish just before the next is enqueued.
func TestBarrierNeedsQuietPolls(t *testing.T) {
	tgt := &pollTarget{busy: []bool{false, true, false}}
	if err := newBarrierRunner(t, tgt).waitIdle(context.Background(), "b", "c"); err != nil {
		t.Fatal(err)
	}
	if tgt.polls != 4 {
		t.Fatalf("polls = %d, want 4 (idle, busy, idle, idle)", tgt.polls)
	}
}

func TestBarrierTimeout(t *testing.T) {
	err := newBarrierRunner(t, &pollTarget{alwaysBusy: true}).waitIdle(context.Background(), "b", "c")
	if err == nil || !strings.Contains(err.Error(), "not idle after 10s") {
		t.Fatalf("err = %v, want a barrier timeout", err)
	}
}
