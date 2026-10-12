package memq

import (
	"context"
	"fmt"
	"memd/internal/ingest"
	"sync"
	"time"
)

// Defaults of a new Queue; see [WithLease] and [WithPollWait].
const (
	defaultLease    = 30 * time.Second
	defaultPollWait = 100 * time.Millisecond
)

// Option configures a [Queue].
type Option func(*Queue)

// WithLease sets how long a delivered message stays invisible before it is redelivered unsettled (default 30 s).
func WithLease(d time.Duration) Option { return func(q *Queue) { q.lease = d } }

// WithPollWait sets how long Receive waits for a message before it returns an empty slice (default 100 ms).
func WithPollWait(d time.Duration) Option { return func(q *Queue) { q.pollWait = d } }

// DeadLetter is one message moved to the dead-letter destination, with the reason it was moved for.
type DeadLetter struct {
	// Message is the message as it was delivered.
	Message ingest.Message
	// Reason is the text given to DeadLetter.
	Reason string
}

// Queue is the in-memory broker: an ordered list of unsettled messages and a dead-letter list. Publish adds to it;
// Source opens a consumer. Messages with the same non-empty Key are strictly ordered: none is delivered while an
// earlier one of its key is in flight or waiting out a Nack delay. All methods are safe for concurrent use.
type Queue struct {
	lease    time.Duration
	pollWait time.Duration

	mu      sync.Mutex
	entries []*entry
	dead    []DeadLetter
	nextID  uint64
	wake    chan struct{} // closed, then replaced, whenever a message may have become deliverable
}

// New returns an empty queue.
func New(opts ...Option) *Queue {
	q := &Queue{lease: defaultLease, pollWait: defaultPollWait, wake: make(chan struct{})}
	for _, o := range opts {
		o(q)
	}
	return q
}

// entry is one unsettled message. token changes on every delivery, nack, expiry and close, so a handle from an
// earlier delivery no longer matches.
type entry struct {
	msg        ingest.Message
	attempt    int
	token      uint64
	inflight   bool
	visibleAt  time.Time
	leaseUntil time.Time
	owner      *Source
}

// handle is the settlement token stored in ingest.Message.Handle.
type handle struct {
	e     *entry
	token uint64
}

// Publish appends messages to the queue. A message without an ID gets one.
func (q *Queue) Publish(msgs ...ingest.Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, m := range msgs {
		q.nextID++
		if m.ID == "" {
			m.ID = fmt.Sprintf("memq-%d", q.nextID)
		}
		q.entries = append(q.entries, &entry{msg: m, visibleAt: time.Now()})
	}
	q.signal()
}

// DeadLetters returns a copy of the dead-letter list, oldest first.
func (q *Queue) DeadLetters() []DeadLetter {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]DeadLetter(nil), q.dead...)
}

// Pending reports how many messages are not settled yet (waiting, in flight or delayed).
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// Source opens a consumer on the queue. Several sources may be open; each message is delivered to one at a time.
func (q *Queue) Source() *Source { return &Source{q: q} }

// signal wakes every waiting Receive. The caller holds q.mu.
func (q *Queue) signal() {
	close(q.wake)
	q.wake = make(chan struct{})
}

// release makes e deliverable again at visibleAt and invalidates every handle to it. The caller holds q.mu.
func (q *Queue) release(e *entry, visibleAt time.Time) {
	e.inflight = false
	e.owner = nil
	e.token++
	e.visibleAt = visibleAt
}

// Source is one consumer of a [Queue].
type Source struct {
	q      *Queue
	closed bool // guarded by q.mu
}

var _ ingest.Source = (*Source)(nil)

// batch is the result of one pass over the queue: the deliveries, the channel that signals new work, and the
// earliest time a waiting message becomes deliverable (zero when none will on its own).
type batch struct {
	msgs []ingest.Message
	wake <-chan struct{}
	next time.Time
}

// Receive implements [ingest.Source].
func (s *Source) Receive(ctx context.Context, limit int) ([]ingest.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memq: receive: %w", err)
	}
	deadline := time.Now().Add(s.q.pollWait)
	for {
		b, err := s.take(limit)
		if err != nil || len(b.msgs) > 0 {
			return b.msgs, err
		}
		until := deadline
		if !b.next.IsZero() && b.next.Before(until) {
			until = b.next
		}
		timer := time.NewTimer(time.Until(until))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("memq: receive: %w", ctx.Err())
		case <-b.wake:
			timer.Stop()
		case <-timer.C:
		}
		if !time.Now().Before(deadline) {
			return nil, nil
		}
	}
}

// take makes one pass over the queue, in publish order, and delivers up to limit ready messages. A keyed message
// blocks every later message of its key, whether it was delivered, is in flight or is delayed.
func (s *Source) take(limit int) (batch, error) {
	q := s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	b := batch{wake: q.wake}
	if s.closed {
		return b, ingest.ErrClosed
	}
	now := time.Now()
	blocked := map[string]bool{}
	for _, e := range q.entries {
		if e.inflight && !e.leaseUntil.After(now) {
			q.release(e, now)
		}
		if blocked[e.msg.Key] && e.msg.Key != "" {
			continue
		}
		blocked[e.msg.Key] = true
		switch {
		case e.inflight:
			b.noteNext(e.leaseUntil)
		case e.visibleAt.After(now):
			b.noteNext(e.visibleAt)
		case len(b.msgs) < limit:
			b.msgs = append(b.msgs, s.deliver(e, now))
		}
	}
	return b, nil
}

// noteNext records t as the time to look again when it is the earliest so far.
func (b *batch) noteNext(t time.Time) {
	if b.next.IsZero() || t.Before(b.next) {
		b.next = t
	}
}

// deliver marks e in flight for this source and returns the message to hand out. The caller holds q.mu.
func (s *Source) deliver(e *entry, now time.Time) ingest.Message {
	e.token++
	e.attempt++
	e.inflight = true
	e.owner = s
	e.leaseUntil = now.Add(s.q.lease)
	m := e.msg
	m.Attempt = e.attempt
	m.Handle = handle{e: e, token: e.token}
	return m
}

// current returns the entries whose handles are still valid: delivered, not settled since, lease not reissued.
// The caller holds q.mu.
func (s *Source) current(msgs []ingest.Message) []*entry {
	var out []*entry
	for _, m := range msgs {
		h, ok := m.Handle.(handle)
		if ok && h.e.inflight && h.e.token == h.token && h.e.owner == s && !contains(out, h.e) {
			out = append(out, h.e)
		}
	}
	return out
}

// remove drops the given entries from the queue. The caller holds q.mu.
func (q *Queue) remove(drop []*entry) {
	gone := make(map[*entry]bool, len(drop))
	for _, e := range drop {
		gone[e] = true
	}
	kept := q.entries[:0]
	for _, e := range q.entries {
		if !gone[e] {
			kept = append(kept, e)
		}
	}
	q.entries = kept
	q.signal()
}

// Ack implements [ingest.Source].
func (s *Source) Ack(_ context.Context, msgs ...ingest.Message) error {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	s.q.remove(s.current(msgs))
	return nil
}

// Nack implements [ingest.Source]: the message is redelivered after delay, ahead of later messages of its key.
func (s *Source) Nack(_ context.Context, delay time.Duration, msgs ...ingest.Message) error {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	at := time.Now().Add(delay)
	for _, e := range s.current(msgs) {
		s.q.release(e, at)
	}
	s.q.signal()
	return nil
}

// DeadLetter implements [ingest.Source]: the messages move to [Queue.DeadLetters].
func (s *Source) DeadLetter(_ context.Context, reason string, msgs ...ingest.Message) error {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	live := s.current(msgs)
	moved := map[*entry]bool{}
	for _, m := range msgs {
		if h, ok := m.Handle.(handle); ok && contains(live, h.e) && !moved[h.e] {
			moved[h.e] = true
			s.q.dead = append(s.q.dead, DeadLetter{Message: m, Reason: reason})
		}
	}
	s.q.remove(live)
	return nil
}

// contains reports whether es holds e.
func contains(es []*entry, e *entry) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}

// Close implements [ingest.Source]: every message this source holds unsettled becomes deliverable again at once.
func (s *Source) Close() error {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	now := time.Now()
	for _, e := range s.q.entries {
		if e.inflight && e.owner == s {
			s.q.release(e, now)
		}
	}
	s.q.signal()
	return nil
}
