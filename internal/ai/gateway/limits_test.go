package gateway_test

import (
	"fmt"
	"memd/internal/ai"
	"memd/internal/ai/gateway"
	"memd/internal/domain"
	"net/http"
	"sync"
	"testing"
	"time"
)

// inflight tracks concurrent requests per key and overall, remembering the peaks.
type inflight struct {
	mu      sync.Mutex
	cur     map[string]int
	peak    map[string]int
	total   int
	maxSeen int
}

// newInflight returns an empty tracker.
func newInflight() *inflight { return &inflight{cur: map[string]int{}, peak: map[string]int{}} }

// enter counts one request of key.
func (f *inflight) enter(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur[key]++
	f.peak[key] = max(f.peak[key], f.cur[key])
	f.total++
	f.maxSeen = max(f.maxSeen, f.total)
}

// exit ends one request of key.
func (f *inflight) exit(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur[key]--
	f.total--
}

// chatRequest is a plain chat request on slot with model m.
func chatRequest(slot domain.ModelSlot, m string) ai.ChatRequest {
	return ai.ChatRequest{
		Slot: slot, Binding: binding(m), Messages: []ai.ChatMessage{{Role: ai.ChatRoleUser, Content: "x"}},
	}
}

// runConcurrently runs fn(i) for i in [0, n) in n goroutines and fails the test on any error.
func runConcurrently(t *testing.T, n int, fn func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			if err := fn(i); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestGlobalSemaphoreCapsInFlight(t *testing.T) {
	const limit, callers = 5, 100
	g := newFakeGateway(t)
	track := newInflight()
	g.onEnter, g.onExit = func() { track.enter("all") }, func() { track.exit("all") }
	g.delay = 20 * time.Millisecond
	g.chat = func(int, map[string]any) (int, any) { return http.StatusOK, chatReply("ok", 1, 1) }
	g.embed = embedEcho(0)
	c := g.client(gateway.Config{MaxConcurrent: limit})
	runConcurrently(t, callers, func(i int) error {
		if i%2 == 0 {
			if _, err := c.EmbedQuery(t.Context(), "q"); err != nil {
				return fmt.Errorf("embed %d: %w", i, err)
			}
			return nil
		}
		if _, err := c.Chat(t.Context(), chatRequest(domain.ModelSlotReflect, "m")); err != nil {
			return fmt.Errorf("chat %d: %w", i, err)
		}
		return nil
	})
	if track.maxSeen != limit {
		t.Errorf("peak in-flight = %d, want exactly %d (capped, and the cap is reached)", track.maxSeen, limit)
	}
	if n := len(g.bodies("chat")) + len(g.bodies("embed")); n != callers {
		t.Errorf("served %d calls, want %d", n, callers)
	}
}

func TestSlotSemaphoreCapsInFlight(t *testing.T) {
	const global, slotLimit, callers = 12, 2, 100
	g := newFakeGateway(t)
	track := newInflight()
	g.delay = 20 * time.Millisecond
	g.onEnter, g.onExit = func() { track.enter("all") }, func() { track.exit("all") }
	c := g.client(gateway.Config{
		MaxConcurrent: global, SlotConcurrency: map[domain.ModelSlot]int{domain.ModelSlotExtract: slotLimit},
	})
	extract := newInflight()
	g.chat = func(_ int, body map[string]any) (int, any) {
		if body["model"] == "m-extract" {
			extract.enter("extract")
			time.Sleep(20 * time.Millisecond)
			extract.exit("extract")
		}
		return http.StatusOK, chatReply("ok", 1, 1)
	}
	runConcurrently(t, callers, func(i int) error {
		slot, model := domain.ModelSlotReflect, "m-reflect"
		if i%2 == 0 {
			slot, model = domain.ModelSlotExtract, "m-extract"
		}
		if _, err := c.Chat(t.Context(), chatRequest(slot, model)); err != nil {
			return fmt.Errorf("chat %d: %w", i, err)
		}
		return nil
	})
	if extract.peak["extract"] != slotLimit {
		t.Errorf("extract peak in-flight = %d, want exactly %d", extract.peak["extract"], slotLimit)
	}
	if track.maxSeen > global {
		t.Errorf("overall peak in-flight = %d, want <= %d", track.maxSeen, global)
	}
	if track.maxSeen <= slotLimit {
		t.Errorf("overall peak in-flight = %d: the unlimited slot should run alongside", track.maxSeen)
	}
}
