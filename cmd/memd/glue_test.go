package main

import (
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"testing"
)

// TestListEventsDefaultAndCap: ListEvents with limit 0 returns the server default (100), not the whole
// history; a limit above the cap returns 1000; a small limit is kept (decision 2502).
func TestListEventsDefaultAndCap(t *testing.T) {
	db := pgtest.NewStore(t)
	b := domain.BankRef{Tenant: "t", Bank: "events"}
	const total = maxEventsLimit + 50
	for range total { // one seq per event, so the soft limit never extends a page over a seq group
		if err := db.InTx(t.Context(), func(tx store.Tx) error {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
			seq, err := tx.Seq(t.Context(), b)
			if err != nil {
				return err
			}
			return tx.Events().Append(t.Context(), domain.LifecycleEvent{
				Bank: b, Seq: seq, Type: domain.EventTypeRetainCompleted,
			})
		}); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}
	o := &operationReader{s: db}
	for _, tc := range []struct{ limit, want int }{
		{0, defaultEventsLimit}, {-1, defaultEventsLimit}, {3, 3}, {total, maxEventsLimit},
	} {
		events, _, err := o.ListEvents(t.Context(), b, 0, tc.limit, nil)
		if err != nil {
			t.Fatalf("ListEvents(limit %d): %v", tc.limit, err)
		}
		if len(events) != tc.want {
			t.Errorf("ListEvents(limit %d) returned %d events, want %d", tc.limit, len(events), tc.want)
		}
	}
}
