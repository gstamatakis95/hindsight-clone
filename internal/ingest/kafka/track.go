package kafka

import (
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// state is where a fetched record is in its life.
type state uint8

const (
	// waiting: fetched or nacked, deliverable once visibleAt has passed and no earlier record of its key is open.
	waiting state = iota
	// inflight: handed out by Receive and not settled yet.
	inflight
	// settled: acked or dead-lettered; it only waits for the records before it so the offset can be committed.
	settled
)

// entry is one fetched record of an assigned partition. token changes on every delivery, settlement and nack, so
// a handle from an earlier delivery no longer matches.
type entry struct {
	p         *partition
	rec       *kgo.Record
	state     state
	token     uint64
	attempt   int
	visibleAt time.Time
}

// partition is the source's bookkeeping for one assigned partition: the records fetched and not yet committed,
// in offset order, and the commit watermark.
type partition struct {
	id int32
	// entries are the tracked records in offset order. The settled prefix is popped as soon as it forms, so
	// entries[0], when present, is the oldest unsettled record.
	entries []*entry
	// next is the offset to commit: the offset after the last popped record, with that record's leader epoch.
	// Offset -1 means nothing was popped yet.
	next kgo.EpochOffset
	// committed is the offset this consumer last committed for the partition, -1 when none.
	committed int64
	// paused is set while fetching is paused because the partition holds MaxBuffered records.
	paused bool
	// gone is set when the partition was revoked or lost, or the source closed: its handles no longer settle.
	gone bool
}

// newPartition returns the bookkeeping of a newly assigned partition.
func newPartition(id int32) *partition {
	return &partition{id: id, next: kgo.EpochOffset{Epoch: -1, Offset: -1}, committed: -1}
}

// add tracks a fetched record. Records arrive in offset order, possibly with gaps (control records, compaction).
func (p *partition) add(rec *kgo.Record) {
	p.entries = append(p.entries, &entry{p: p, rec: rec})
}

// advance pops the settled prefix and moves the commit watermark past it. The watermark is the offset after the
// last popped record, never "watermark + 1", because offsets have gaps.
func (p *partition) advance() {
	n := 0
	for n < len(p.entries) && p.entries[n].state == settled {
		rec := p.entries[n].rec
		p.next = kgo.EpochOffset{Epoch: rec.LeaderEpoch, Offset: rec.Offset + 1}
		n++
	}
	if n > 0 {
		clear(p.entries[:n])
		p.entries = p.entries[n:]
	}
}

// due returns the watermark when it is past what this consumer last committed.
func (p *partition) due() (kgo.EpochOffset, bool) {
	return p.next, p.next.Offset > p.committed
}

// ready returns up to room entries that may be delivered now, in offset order, and the earliest time a delayed
// entry whose key is free becomes deliverable (zero when none). A record with a non-empty key waits while an
// earlier record of its key is in flight or delayed, so one key never has two records open at once.
func (p *partition) ready(now time.Time, room int) (out []*entry, next time.Time) {
	busy := map[string]bool{}
	for _, e := range p.entries {
		if len(out) >= room {
			break
		}
		if e.state == settled {
			continue
		}
		if k := string(e.rec.Key); k != "" {
			if busy[k] {
				continue
			}
			busy[k] = true
		}
		switch {
		case e.state == inflight:
		case e.visibleAt.After(now):
			if next.IsZero() || e.visibleAt.Before(next) {
				next = e.visibleAt
			}
		default:
			out = append(out, e)
		}
	}
	return out, next
}
