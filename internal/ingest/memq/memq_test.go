package memq_test

import (
	"memd/internal/ingest"
	"memd/internal/ingest/ingesttest"
	"memd/internal/ingest/memq"
	"testing"
	"time"
)

// TestContract runs the shared contract suite against the in-memory source.
func TestContract(t *testing.T) {
	const lease = 150 * time.Millisecond
	ingesttest.Run(t, func(*testing.T) ingesttest.Harness {
		q := memq.New(memq.WithLease(lease), memq.WithPollWait(20*time.Millisecond))
		return ingesttest.Harness{
			Open:    func(*testing.T) ingest.Source { return q.Source() },
			Publish: func(_ *testing.T, msgs ...ingest.Message) { q.Publish(msgs...) },
			DeadLetters: func(*testing.T) []ingest.Message {
				var out []ingest.Message
				for _, d := range q.DeadLetters() {
					out = append(out, d.Message)
				}
				return out
			},
			LeaseTimeout: lease,
		}
	})
}

// TestNackKeepsKeyOrder pins memq's stricter rule: a delayed message holds back later messages of its key.
func TestNackKeepsKeyOrder(t *testing.T) {
	q := memq.New(memq.WithPollWait(20 * time.Millisecond))
	q.Publish(ingest.Message{Key: "k", Body: []byte("first")}, ingest.Message{Key: "k", Body: []byte("second")})
	src := q.Source()
	defer func() { _ = src.Close() }()
	got, err := src.Receive(t.Context(), 10)
	if err != nil || len(got) != 1 || string(got[0].Body) != "first" {
		t.Fatalf("Receive = %v, %v; want only the first message of the key", got, err)
	}
	if err := src.Nack(t.Context(), 100*time.Millisecond, got[0]); err != nil {
		t.Fatal(err)
	}
	if early, _ := src.Receive(t.Context(), 10); len(early) != 0 {
		t.Fatalf("a later message of the key overtook the nacked one: %v", early)
	}
	var again []ingest.Message
	for end := time.Now().Add(5 * time.Second); len(again) == 0 && time.Now().Before(end); {
		if again, err = src.Receive(t.Context(), 10); err != nil {
			t.Fatal(err)
		}
	}
	if len(again) != 1 || string(again[0].Body) != "first" || again[0].Attempt != 2 {
		t.Fatalf("redelivery = %v; want the first message, attempt 2", again)
	}
}
