package ingesttest

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/ingest"
	"testing"
	"time"
)

const (
	// waitTimeout bounds every wait for a delivery that must happen.
	waitTimeout = 20 * time.Second
	// quietWindow is how long a source is watched to prove that nothing is delivered.
	quietWindow = 400 * time.Millisecond
	// nackDelay is the redelivery delay the Nack test asks for.
	nackDelay = 300 * time.Millisecond
)

// Harness gives the suite one fresh, empty subscription to test. Every case calls the constructor again, so cases
// never share messages.
type Harness struct {
	// Open returns a consumer on the subscription. Each call returns a new consumer; the messages a closed consumer
	// left unsettled go back to the subscription.
	Open func(t *testing.T) ingest.Source
	// Publish puts messages on the subscription, in order.
	Publish func(t *testing.T, msgs ...ingest.Message)
	// DeadLetters returns the messages that reached the dead-letter destination so far.
	DeadLetters func(t *testing.T) []ingest.Message
	// LeaseTimeout is how long an unsettled message of an open consumer stays invisible before it is redelivered.
	// Zero means the adapter has no lease expiry and the lease case is skipped.
	LeaseTimeout time.Duration
}

// Run runs the contract suite: each case gets its own harness from newHarness and runs in parallel with the others.
// It checks that unsettled messages are redelivered (after Close and after the lease), that Nack delays redelivery,
// that a dead-lettered message never returns on the main subscription, that same-key messages arrive in publish
// order, that settling is idempotent and a stale settlement cannot settle a redelivery, and Receive's edge cases.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, h Harness)
	}{
		{"ReceiveIdle", testReceiveIdle},
		{"ReceiveHonoursMax", testReceiveMax},
		{"ReceiveContextCancelled", testReceiveCancelled},
		{"ReceiveAfterClose", testReceiveAfterClose},
		{"AckSettlesForGood", testAck},
		{"UnsettledRedeliveredAfterClose", testRedeliverAfterClose},
		{"UnsettledRedeliveredAfterLease", testRedeliverAfterLease},
		{"NackDelaysRedelivery", testNack},
		{"DeadLetterNeverReturns", testDeadLetter},
		{"SameKeyKeepsPublishOrder", testKeyOrder},
		{"StaleSettlementIsHarmless", testStaleSettlement},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.fn(t, newHarness(t))
		})
	}
}

// msg builds a message with the given key and body.
func msg(key, body string) ingest.Message {
	attrs := map[string]string{ingest.AttrContentType: ingest.ContentTypeJSON}
	return ingest.Message{Key: key, Body: []byte(body), Attributes: attrs}
}

// bodies returns the bodies of msgs as strings.
func bodies(msgs []ingest.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Body)
	}
	return out
}

// receiveN polls src until it has delivered n messages, and fails the test on a Receive error or a timeout.
func receiveN(t *testing.T, src ingest.Source, n int) []ingest.Message {
	t.Helper()
	var got []ingest.Message
	deadline := time.Now().Add(waitTimeout)
	for len(got) < n {
		if time.Now().After(deadline) {
			t.Fatalf("received %d of %d messages before the timeout: %v", len(got), n, bodies(got))
		}
		batch, err := src.Receive(t.Context(), n-len(got))
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		got = append(got, batch...)
	}
	return got
}

// expectQuiet fails the test if src delivers anything during the quiet window.
func expectQuiet(t *testing.T, src ingest.Source) {
	t.Helper()
	end := time.Now().Add(quietWindow)
	for time.Now().Before(end) {
		batch, err := src.Receive(t.Context(), 10)
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if len(batch) > 0 {
			t.Fatalf("unexpected delivery: %v", bodies(batch))
		}
	}
}

// closeSource closes src and fails the test on error.
func closeSource(t *testing.T, src ingest.Source) {
	t.Helper()
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// wantOne fails the test unless got is exactly one message with the given body.
func wantOne(t *testing.T, got []ingest.Message, body string) ingest.Message {
	t.Helper()
	if len(got) != 1 || string(got[0].Body) != body {
		t.Fatalf("got %v, want one message %q", bodies(got), body)
	}
	return got[0]
}

func testReceiveIdle(t *testing.T, h Harness) {
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	got, err := src.Receive(t.Context(), 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("Receive on an empty subscription = %v, %v; want no messages and no error", bodies(got), err)
	}
}

func testReceiveMax(t *testing.T, h Harness) {
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	var all []ingest.Message
	for i := range 5 {
		all = append(all, msg(fmt.Sprintf("k%d", i), fmt.Sprintf("m%d", i)))
	}
	h.Publish(t, all...)
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		got, err := src.Receive(t.Context(), 2)
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if len(got) > 2 {
			t.Fatalf("Receive(2) returned %d messages", len(got))
		}
		if len(got) > 0 {
			return
		}
	}
	t.Fatal("no message delivered before the timeout")
}

func testReceiveCancelled(t *testing.T, h Harness) {
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := src.Receive(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive with a cancelled context = %v, want context.Canceled", err)
	}
}

func testReceiveAfterClose(t *testing.T, h Harness) {
	src := h.Open(t)
	closeSource(t, src)
	if _, err := src.Receive(t.Context(), 10); !errors.Is(err, ingest.ErrClosed) {
		t.Fatalf("Receive after Close = %v, want ingest.ErrClosed", err)
	}
}

func testAck(t *testing.T, h Harness) {
	h.Publish(t, msg("", "a"))
	src := h.Open(t)
	m := wantOne(t, receiveN(t, src, 1), "a")
	for range 2 { // acking twice is not an error
		if err := src.Ack(t.Context(), m); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	closeSource(t, src)
	next := h.Open(t)
	defer func() { _ = next.Close() }()
	expectQuiet(t, next)
}

func testRedeliverAfterClose(t *testing.T, h Harness) {
	h.Publish(t, msg("", "a"))
	first := h.Open(t)
	wantOne(t, receiveN(t, first, 1), "a")
	closeSource(t, first)
	second := h.Open(t)
	defer func() { _ = second.Close() }()
	wantOne(t, receiveN(t, second, 1), "a")
}

func testRedeliverAfterLease(t *testing.T, h Harness) {
	if h.LeaseTimeout == 0 {
		t.Skip("the adapter has no lease expiry")
	}
	h.Publish(t, msg("", "a"))
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	wantOne(t, receiveN(t, src, 1), "a")
	wantOne(t, receiveN(t, src, 1), "a") // never settled, so it comes back to the same consumer
}

func testNack(t *testing.T, h Harness) {
	h.Publish(t, msg("", "a"))
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	m := wantOne(t, receiveN(t, src, 1), "a")
	nacked := time.Now()
	if err := src.Nack(t.Context(), nackDelay, m); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	wantOne(t, receiveN(t, src, 1), "a")
	if elapsed := time.Since(nacked); elapsed < nackDelay {
		t.Fatalf("redelivered after %v, before the %v delay", elapsed, nackDelay)
	}
}

func testDeadLetter(t *testing.T, h Harness) {
	h.Publish(t, msg("", "a"), msg("", "b"))
	src := h.Open(t)
	got := receiveN(t, src, 2)
	if err := src.DeadLetter(t.Context(), "undecodable", got[0]); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	if err := src.Ack(t.Context(), got[1]); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	closeSource(t, src)
	next := h.Open(t)
	defer func() { _ = next.Close() }()
	expectQuiet(t, next)
	if dead := h.DeadLetters(t); len(dead) != 1 || string(dead[0].Body) != string(got[0].Body) {
		t.Fatalf("dead letters = %v, want the one message %q", bodies(dead), got[0].Body)
	}
}

func testKeyOrder(t *testing.T, h Harness) {
	const perKey = 4
	var all []ingest.Message
	for i := range perKey {
		all = append(all, msg("a", fmt.Sprintf("a%d", i)), msg("b", fmt.Sprintf("b%d", i)))
	}
	h.Publish(t, all...)
	src := h.Open(t)
	defer func() { _ = src.Close() }()
	seen := map[string][]string{}
	for total := 0; total < 2*perKey; {
		batch := receiveN(t, src, 1)
		for _, m := range batch {
			seen[m.Key] = append(seen[m.Key], string(m.Body))
		}
		total += len(batch)
		if err := src.Ack(t.Context(), batch...); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	for _, key := range []string{"a", "b"} {
		for i, body := range seen[key] {
			if want := fmt.Sprintf("%s%d", key, i); body != want {
				t.Fatalf("key %q arrived as %v, want publish order", key, seen[key])
			}
		}
	}
}

func testStaleSettlement(t *testing.T, h Harness) {
	h.Publish(t, msg("", "a"))
	first := h.Open(t)
	stale := wantOne(t, receiveN(t, first, 1), "a")
	closeSource(t, first)
	second := h.Open(t)
	wantOne(t, receiveN(t, second, 1), "a")
	if err := first.Ack(t.Context(), stale); err != nil { // the lease is gone: not an error, and not a settlement
		t.Fatalf("stale Ack: %v", err)
	}
	closeSource(t, second)
	third := h.Open(t)
	defer func() { _ = third.Close() }()
	wantOne(t, receiveN(t, third, 1), "a")
}
