package fake_test

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"time"
)

// ExampleStore shows the usual engine-test setup: a store on a frozen clock, a bank, one transaction that
// writes a unit stamped with the transaction's Seq together with the job it causes, and a read through
// Read().
func ExampleStore() {
	ctx := context.Background()
	clock := fake.NewClock(time.Time{})
	store := fake.NewStore(clock)
	bank := port.BankRef{Tenant: "acme", Bank: "notes"}

	err := store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, bank); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, bank) // every tx that changes memory takes the bank's change seq
		if err != nil {
			return err
		}
		u := domain.Unit{ID: "u1", Bank: bank, Type: domain.FactTypeWorld, Text: "Alice likes tea", Seq: seq}
		if err := tx.Units().Insert(ctx, []domain.Unit{u}); err != nil {
			return err
		}
		_, _, err = tx.Queue().Enqueue(ctx, port.Job{
			Bank: bank, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Dedupe: port.DedupePending,
		})
		return err
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	units, _ := store.Read().GetUnits(ctx, bank, []port.UnitID{"u1"})
	seq, _ := store.Read().BankSeq(ctx, bank)
	fmt.Println(units[0].Text, units[0].Seq, seq)
	// Output: Alice likes tea 1 1
}

// ExampleStore_claimer shows the worker side: the Store is also the port.Claimer, and its due-ness and
// heartbeat staleness follow the injected Clock.
func ExampleStore_claimer() {
	ctx := context.Background()
	clock := fake.NewClock(time.Time{})
	store := fake.NewStore(clock)
	bank := port.BankRef{Tenant: "acme", Bank: "notes"}
	var claimer port.Claimer = store

	_ = store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, bank); err != nil {
			return err
		}
		_, _, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank: bank, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:team",
			Payload: json.RawMessage(`{"model_id":"team"}`), RunAt: clock.Now().Add(time.Minute), // debounced
		})
		return err
	})
	kinds := []port.JobKind{domain.OperationKindRefreshMentalModel}
	jobs, _ := claimer.Claim(ctx, "worker-1", kinds, 10)
	fmt.Println("due now:", len(jobs))

	clock.Advance(time.Minute)
	jobs, _ = claimer.Claim(ctx, "worker-1", kinds, 10)
	fmt.Println("due after a minute:", len(jobs), string(jobs[0].Payload))

	clock.Advance(5 * time.Minute) // no heartbeat for 5 minutes
	n, _ := claimer.ReclaimStale(ctx, 2*time.Minute)
	alive, _ := claimer.Alive(ctx, jobs[0].ID)
	fmt.Println("reclaimed:", n, "alive:", alive)
	// Output:
	// due now: 0
	// due after a minute: 1 {"model_id":"team"}
	// reclaimed: 1 alive: false
}
