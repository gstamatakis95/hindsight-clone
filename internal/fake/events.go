package fake

import (
	"cmp"
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// eventRepo is the EventRepo of one transaction.
type eventRepo struct{ t *tx }

var _ port.EventRepo = eventRepo{}

// Append stores e. Its Seq must be the change seq this transaction took for e.Bank with Tx.Seq; anything
// else is domain.ErrInvalidArgument, so an engine that forgets to stamp its events fails loudly. A zero At
// becomes the transaction time.
func (r eventRepo) Append(_ context.Context, e domain.LifecycleEvent) error {
	seq, ok := r.t.seqs[e.Bank]
	if !ok || e.Seq != seq {
		return fmt.Errorf("%w: event seq %d is not this transaction's change seq for bank %s/%s (Tx.Seq first)",
			domain.ErrInvalidArgument, e.Seq, e.Bank.Tenant, e.Bank.Bank)
	}
	r.t.st.appendEvent(e, r.t.now)
	return nil
}

// appendEvent stores e, defaulting its time to now.
func (s *state) appendEvent(e domain.LifecycleEvent, now time.Time) {
	if e.At.IsZero() {
		e.At = now
	}
	s.events = append(s.events, eventRow{e: e, order: s.next()})
}

// List returns events of b with seq > afterSeq in seq order, keeping only types (empty = all). It stops
// after the seq group in which the number of returned events reaches limit (limit <= 0 = no limit), so a
// page never splits one transaction's events. lastSeq is the highest seq scanned, never below afterSeq.
func (r eventRepo) List(
	_ context.Context, b port.BankRef, afterSeq int64, limit int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64, error) {
	var rows []eventRow
	for _, row := range r.t.st.events {
		if row.e.Bank == b && row.e.Seq > afterSeq {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(x, y eventRow) int {
		return cmp.Or(cmp.Compare(x.e.Seq, y.e.Seq), cmp.Compare(x.order, y.order))
	})
	var out []domain.LifecycleEvent
	lastSeq := afterSeq
	for i, row := range rows {
		if limit > 0 && len(out) >= limit && row.e.Seq != rows[i-1].e.Seq {
			break
		}
		lastSeq = row.e.Seq
		if len(types) == 0 || slices.Contains(types, row.e.Type) {
			out = append(out, row.e)
		}
	}
	return out, lastSeq, nil
}

// Prune deletes events whose At is before before, across banks, and returns how many.
func (r eventRepo) Prune(_ context.Context, before time.Time) (int, error) {
	return deleteSlice(&r.t.st.events, len(r.t.st.events), func(v eventRow) bool { return v.e.At.Before(before) }), nil
}
