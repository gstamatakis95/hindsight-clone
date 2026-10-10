package fake_test

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"
)

// TestEnqueueUpgradeRace: a claim that starts between a joiner's Enqueue and its Upgrade waits for the
// joiner's commit, so the claimed job carries the merged payload (no claim lands between join and Upgrade).
func TestEnqueueUpgradeRace(t *testing.T) {
	f := newFixture(t)
	refresh := port.Job{
		Bank: mine, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:a",
		Payload: json.RawMessage(`{"model_id":"a"}`),
	}
	id := f.enqueue(refresh)
	joined, claimed := make(chan struct{}), make(chan []port.Job, 1)
	go func() {
		<-joined
		js, _ := f.s.Claim(context.Background(), "w", []port.JobKind{refresh.Kind}, 10)
		claimed <- js
	}()
	f.tx(func(tx port.Tx) error {
		got, ok, err := tx.Queue().Enqueue(t.Context(), refresh)
		if err != nil || !ok || got != id {
			t.Errorf("joiner Enqueue = %s, %v, %v; want to join %s", got, ok, err, id)
		}
		close(joined)
		select {
		case js := <-claimed:
			t.Errorf("Claim returned %d jobs while the joiner's transaction was open", len(js))
		case <-time.After(50 * time.Millisecond):
		}
		return tx.Queue().Upgrade(t.Context(), id, json.RawMessage(`{"force_full":true}`), nil)
	})
	js := <-claimed
	if len(js) != 1 || string(js[0].Payload) != `{"force_full":true,"model_id":"a"}` {
		t.Errorf("claimed = %+v, want the upgraded refresh", js)
	}
}

// TestTxUnusableAfterCommit: a Tx kept past its InTx panics instead of writing into committed state.
func TestTxUnusableAfterCommit(t *testing.T) {
	f := newFixture(t)
	var kept port.Tx
	f.tx(func(tx port.Tx) error { kept = tx; return nil })
	defer func() {
		if recover() == nil {
			t.Errorf("using a Tx after its InTx returned did not panic")
		}
	}()
	_, _ = kept.Banks().Ensure(t.Context(), port.BankRef{Tenant: "t3", Bank: "b"})
}
