package kafka

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/ingest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Headers a dead-lettered record carries on top of the original record's headers. Headers with HeaderPrefix on the
// original record are replaced, so a record dead-lettered twice carries only the latest failure.
const (
	// HeaderPrefix starts every header the source adds to a dead-lettered record.
	HeaderPrefix = "memd-dlq-"
	// HeaderReason is the reason given to DeadLetter.
	HeaderReason = HeaderPrefix + "reason"
	// HeaderTopic is the topic the record was consumed from.
	HeaderTopic = HeaderPrefix + "topic"
	// HeaderPartition is the partition the record was consumed from, in decimal.
	HeaderPartition = HeaderPrefix + "partition"
	// HeaderOffset is the record's offset in that partition, in decimal.
	HeaderOffset = HeaderPrefix + "offset"
	// HeaderAttempt is Message.Attempt of the delivery that was dead-lettered, in decimal.
	HeaderAttempt = HeaderPrefix + "attempt"
)

// closeCommitTimeout bounds the final commit of Close and of a revoke, so a broker outage cannot hang a shutdown
// or a rebalance; an offset not committed then is only redelivered.
const closeCommitTimeout = 10 * time.Second

// Source is an ingest.Source over a Kafka consumer group. The package comment states its semantics. All methods
// are safe for concurrent use, with one goroutine calling Receive.
type Source struct {
	cfg Config
	cl  *kgo.Client

	// commitMu serializes commits, revokes and Close, so a commit never moves an offset backwards and never
	// commits a partition after it was revoked. It is taken before mu.
	commitMu sync.Mutex

	mu     sync.Mutex
	closed bool
	parts  map[int32]*partition
	rot    int // rotates the partition Receive serves first, for fairness
}

var _ ingest.Source = (*Source)(nil)

// handle is the settlement token stored in ingest.Message.Handle.
type handle struct {
	src   *Source
	e     *entry
	token uint64
}

// New validates cfg and returns a source; it joins the consumer group in the background. Nothing is fetched
// before the first Receive. An invalid or incomplete cfg (a missing dead-letter topic included) is
// domain.ErrInvalidArgument naming the field.
func New(cfg Config) (*Source, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Source{cfg: cfg.withDefaults(), parts: map[int32]*partition{}}
	cl, err := kgo.NewClient(s.cfg.clientOptions(s.onRevoked, s.onLost)...)
	if err != nil {
		return nil, fmt.Errorf("%w: kafka: new client: %w", domain.ErrInvalidArgument, err)
	}
	s.cl = cl
	return s, nil
}

// Receive implements [ingest.Source]. It first hands out records already fetched, then polls the brokers until
// something is deliverable or Config.PollWait has passed.
func (s *Source) Receive(ctx context.Context, limit int) ([]ingest.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("kafka: receive: %w", err)
	}
	deadline := time.Now().Add(s.cfg.PollWait)
	for {
		msgs, next, err := s.take(limit)
		if err != nil || len(msgs) > 0 {
			return msgs, err
		}
		if !time.Now().Before(deadline) {
			return nil, nil
		}
		until := deadline
		if !next.IsZero() && next.Before(until) {
			until = next
		}
		if err := s.poll(ctx, until, limit); err != nil {
			return nil, err
		}
	}
}

// poll fetches up to limit records, waiting until the given time at most, and tracks them. It allows the
// rebalance the poll blocked once the records are tracked.
func (s *Source) poll(ctx context.Context, until time.Time, limit int) error {
	pctx, cancel := context.WithDeadline(ctx, until)
	fetches := s.cl.PollRecords(pctx, limit)
	cancel()
	err := s.track(fetches)
	s.cl.AllowRebalance()
	if ctx.Err() != nil {
		return fmt.Errorf("kafka: receive: %w", ctx.Err())
	}
	return err
}

// track adds the polled records to their partitions and reports a fetch error worth returning: ErrClosed once
// the client is closed, domain.ErrUnavailable for a non-retriable broker error. Context errors (the poll's own
// deadline) and retriable errors, which the client retries itself, are not errors.
func (s *Source) track(fetches kgo.Fetches) error {
	var ferr error
	fetches.EachError(func(topic string, part int32, err error) {
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), kerr.IsRetriable(err):
		case errors.Is(err, kgo.ErrClientClosed):
			ferr = ingest.ErrClosed
		case ferr == nil:
			ferr = fmt.Errorf("%w: kafka: fetch %s[%d]: %w", domain.ErrUnavailable, topic, part, err)
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ingest.ErrClosed
	}
	fetches.EachRecord(func(rec *kgo.Record) {
		p := s.parts[rec.Partition]
		if p == nil {
			p = newPartition(rec.Partition)
			s.parts[rec.Partition] = p
		}
		p.add(rec)
		s.flow(p)
	})
	return ferr
}

// take hands out up to limit deliverable records, serving the partitions in rotating order, and returns the
// earliest time a delayed record becomes deliverable when it hands out none.
func (s *Source) take(limit int) ([]ingest.Message, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, time.Time{}, ingest.ErrClosed
	}
	ids := make([]int32, 0, len(s.parts))
	for id := range s.parts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	s.rot++
	now := time.Now()
	var out []ingest.Message
	var next time.Time
	for i := range ids {
		ready, at := s.parts[ids[(i+s.rot)%len(ids)]].ready(now, limit-len(out))
		for _, e := range ready {
			out = append(out, s.deliver(e))
		}
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
		if len(out) >= limit {
			break
		}
	}
	return out, next, nil
}

// deliver marks e in flight and returns the message to hand out. The caller holds mu.
func (s *Source) deliver(e *entry) ingest.Message {
	e.state = inflight
	e.token++
	e.attempt++
	rec := e.rec
	var attrs map[string]string
	if len(rec.Headers) > 0 {
		attrs = make(map[string]string, len(rec.Headers))
		for _, h := range rec.Headers {
			attrs[h.Key] = string(h.Value)
		}
	}
	return ingest.Message{
		ID:         fmt.Sprintf("%s/%d/%d", rec.Topic, rec.Partition, rec.Offset),
		Key:        string(rec.Key),
		Body:       rec.Value,
		Attributes: attrs,
		Attempt:    e.attempt,
		Handle:     handle{src: s, e: e, token: e.token},
	}
}

// live returns the distinct entries msgs still hold: delivered by this source, in flight under the same delivery,
// and of a partition still assigned. The caller holds mu.
func (s *Source) live(msgs []ingest.Message) []*entry {
	var out []*entry
	for _, m := range msgs {
		h, ok := m.Handle.(handle)
		if ok && h.src == s && !h.e.p.gone && h.e.state == inflight && h.e.token == h.token &&
			!slices.Contains(out, h.e) {
			out = append(out, h.e)
		}
	}
	return out
}

// settle marks e settled and moves its partition's watermark. The caller holds mu.
func (s *Source) settle(e *entry) {
	e.state = settled
	e.token++
	e.p.advance()
	s.flow(e.p)
}

// flow pauses fetching a partition that holds MaxBuffered records and resumes it once it holds fewer than half.
// The caller holds mu.
func (s *Source) flow(p *partition) {
	switch n := len(p.entries); {
	case !p.paused && n >= s.cfg.MaxBuffered:
		p.paused = true
		s.cl.PauseFetchPartitions(map[string][]int32{s.cfg.Topic: {p.id}})
	case p.paused && n < s.cfg.MaxBuffered/2:
		p.paused = false
		s.cl.ResumeFetchPartitions(map[string][]int32{s.cfg.Topic: {p.id}})
	}
}

// Ack implements [ingest.Source]: the messages are settled, then every partition whose contiguous settled prefix
// grew is committed. A commit failure is domain.ErrUnavailable; the settlement stands, and the next Ack,
// DeadLetter, revoke or Close commits the offset.
func (s *Source) Ack(ctx context.Context, msgs ...ingest.Message) error {
	s.mu.Lock()
	for _, e := range s.live(msgs) {
		s.settle(e)
	}
	s.mu.Unlock()
	return s.commit(ctx)
}

// Nack implements [ingest.Source]: the messages become deliverable again after delay, ahead of every later record
// of their key, while the other keys of the partition keep flowing. Nothing is committed past them meanwhile.
func (s *Source) Nack(_ context.Context, delay time.Duration, msgs ...ingest.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Now().Add(delay)
	for _, e := range s.live(msgs) {
		e.state = waiting
		e.token++
		e.visibleAt = at
	}
	return nil
}

// DeadLetter implements [ingest.Source]: the records are produced to the dead-letter topic with their key, value
// and headers plus the HeaderPrefix headers, and settled once the brokers acknowledged the produce. A produce
// failure is domain.ErrUnavailable and settles nothing.
func (s *Source) DeadLetter(ctx context.Context, reason string, msgs ...ingest.Message) error {
	s.mu.Lock()
	live := s.live(msgs)
	recs := make([]*kgo.Record, len(live))
	tokens := make([]uint64, len(live))
	for i, e := range live {
		recs[i] = deadRecord(e, s.cfg.DeadLetterTopic, reason)
		tokens[i] = e.token
	}
	s.mu.Unlock()
	if len(recs) > 0 {
		if err := s.cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
			return fmt.Errorf("%w: kafka: produce to dead-letter topic %s: %w", domain.ErrUnavailable,
				s.cfg.DeadLetterTopic, err)
		}
	}
	s.mu.Lock()
	for i, e := range live {
		if !e.p.gone && e.state == inflight && e.token == tokens[i] {
			s.settle(e)
		}
	}
	s.mu.Unlock()
	return s.commit(ctx)
}

// deadRecord returns the dead-letter copy of e's record: same key and value, the original headers without any
// HeaderPrefix ones, then the failure headers.
func deadRecord(e *entry, topic, reason string) *kgo.Record {
	rec := e.rec
	headers := make([]kgo.RecordHeader, 0, len(rec.Headers)+5)
	for _, h := range rec.Headers {
		if !strings.HasPrefix(h.Key, HeaderPrefix) {
			headers = append(headers, h)
		}
	}
	headers = append(headers,
		kgo.RecordHeader{Key: HeaderReason, Value: []byte(reason)},
		kgo.RecordHeader{Key: HeaderTopic, Value: []byte(rec.Topic)},
		kgo.RecordHeader{Key: HeaderPartition, Value: []byte(strconv.FormatInt(int64(rec.Partition), 10))},
		kgo.RecordHeader{Key: HeaderOffset, Value: []byte(strconv.FormatInt(rec.Offset, 10))},
		kgo.RecordHeader{Key: HeaderAttempt, Value: []byte(strconv.Itoa(e.attempt))},
	)
	return &kgo.Record{Topic: topic, Key: rec.Key, Value: rec.Value, Headers: headers}
}

// commit commits every partition whose watermark is past its last commit. Holding commitMu while it reads the
// watermarks and commits keeps commits in order and away from revoked partitions.
func (s *Source) commit(ctx context.Context) error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	due := s.dueLocked(nil)
	s.mu.Unlock()
	if len(due) == 0 {
		return nil
	}
	if err := commitOffsets(ctx, s.cl, s.cfg.Topic, due); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, off := range due {
		if p := s.parts[id]; p != nil && off.Offset > p.committed {
			p.committed = off.Offset
		}
	}
	return nil
}

// dueLocked returns the watermarks to commit for the given partitions, or for every partition when ids is nil.
// The caller holds mu.
func (s *Source) dueLocked(ids []int32) map[int32]kgo.EpochOffset {
	due := map[int32]kgo.EpochOffset{}
	for id, p := range s.parts {
		if ids != nil && !slices.Contains(ids, id) {
			continue
		}
		if off, ok := p.due(); ok {
			due[id] = off
		}
	}
	return due
}

// commitOffsets commits offsets for topic synchronously and returns the request's or any partition's error as
// domain.ErrUnavailable.
func commitOffsets(ctx context.Context, cl *kgo.Client, topic string, offsets map[int32]kgo.EpochOffset) error {
	var cerr error
	cl.CommitOffsetsSync(ctx, map[string]map[int32]kgo.EpochOffset{topic: offsets},
		func(_ *kgo.Client, _ *kmsg.OffsetCommitRequest, resp *kmsg.OffsetCommitResponse, err error) {
			if err != nil {
				cerr = err
				return
			}
			for _, t := range resp.Topics {
				for _, p := range t.Partitions {
					if err := kerr.ErrorForCode(p.ErrorCode); err != nil && cerr == nil {
						cerr = err
					}
				}
			}
		})
	if cerr != nil {
		return fmt.Errorf("%w: kafka: commit offsets: %w", domain.ErrUnavailable, cerr)
	}
	return nil
}

// onRevoked is the client's revoke hook: it commits the watermarks of the partitions leaving this consumer and
// forgets them, so their outstanding handles settle nothing.
func (s *Source) onRevoked(ctx context.Context, cl *kgo.Client, revoked map[string][]int32) {
	s.release(ctx, cl, revoked[s.cfg.Topic], true)
}

// onLost is the client's lost hook: the partitions already belong to another member, so they are forgotten
// without a commit.
func (s *Source) onLost(ctx context.Context, cl *kgo.Client, lost map[string][]int32) {
	s.release(ctx, cl, lost[s.cfg.Topic], false)
}

// release forgets the given partitions, resumes the paused ones (a pause outlives the assignment) and, when
// commit is set, commits their watermarks first. A failed commit only means redelivery to the next owner.
func (s *Source) release(ctx context.Context, cl *kgo.Client, ids []int32, commit bool) {
	if len(ids) == 0 {
		return
	}
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	s.mu.Lock()
	due := map[int32]kgo.EpochOffset{}
	if commit {
		due = s.dueLocked(ids)
	}
	var resume []int32
	for _, id := range ids {
		if p := s.parts[id]; p != nil {
			p.gone = true
			if p.paused {
				resume = append(resume, id)
			}
			delete(s.parts, id)
		}
	}
	s.mu.Unlock()
	if len(resume) > 0 {
		cl.ResumeFetchPartitions(map[string][]int32{s.cfg.Topic: resume})
	}
	if len(due) > 0 {
		cctx, cancel := context.WithTimeout(ctx, closeCommitTimeout)
		defer cancel()
		_ = commitOffsets(cctx, cl, s.cfg.Topic, due) // best effort: uncommitted offsets are redelivered
	}
}

// Close implements [ingest.Source]: it commits the settled watermarks, forgets every partition, so later
// settlements are no-ops, and leaves the consumer group, which hands the partitions to the other members at once.
// Unsettled records are redelivered from the committed offsets. A failed final commit is returned as
// domain.ErrUnavailable after the group was left anyway; it only means redelivery.
func (s *Source) Close() error {
	s.commitMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.commitMu.Unlock()
		return nil
	}
	s.closed = true
	due := s.dueLocked(nil)
	for _, p := range s.parts {
		p.gone = true
	}
	s.parts = map[int32]*partition{}
	s.mu.Unlock()
	var err error
	if len(due) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), closeCommitTimeout)
		err = commitOffsets(ctx, s.cl, s.cfg.Topic, due)
		cancel()
	}
	s.commitMu.Unlock()
	s.cl.CloseAllowingRebalance() // runs onRevoked, which finds nothing left; must not hold commitMu
	return err
}
