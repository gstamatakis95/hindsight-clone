package postgres_test

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestQueueGoneBank covers a bank whose row vanished under a processing job, which the ports cannot build
// (BankRepo.Delete removes the bank's operations too): Fail(nil) and ReclaimStale at max attempts still end
// the job FAILED without an event and without error, and the sweep handles the other bank's job.
func TestQueueGoneBank(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	gone := domain.BankRef{Tenant: "t-gone", Bank: "gone"}
	live := domain.BankRef{Tenant: "t-gone", Bank: "live"}
	ensureQueueBanks(t, s, gone, live)
	retain := domain.OperationKindRetainDocument
	failNow := enqueueJob(t, s, store.Job{Bank: gone, Kind: retain, SerialKey: "doc:a", Payload: json.RawMessage(`{}`)})
	atMax := enqueueJob(t, s, store.Job{
		Bank: gone, Kind: retain, SerialKey: "doc:b", Payload: json.RawMessage(`{}`), Attempt: retain.MaxAttempts() - 1,
	})
	other := enqueueJob(t, s, store.Job{Bank: live, Kind: retain, SerialKey: "doc:c", Payload: json.RawMessage(`{}`)})
	if js, err := s.Claim(ctx, "w", []domain.OperationKind{retain}, 10); err != nil || len(js) != 3 {
		t.Fatalf("Claim = %d jobs, %v; want 3", len(js), err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM banks WHERE tenant_id = $1 AND id = $2`, gone.Tenant, gone.Bank); err != nil {
		t.Fatalf("delete bank row: %v", err)
	}
	if err := s.Fail(ctx, failNow, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail(nil) with the bank gone: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	n, err := s.ReclaimStale(ctx, 200*time.Millisecond)
	if err != nil || n != 2 {
		t.Fatalf("ReclaimStale = %d, %v; want 2, nil", n, err)
	}
	wantOpStatus(t, pool, failNow, domain.OperationStatusFailed)
	wantOpStatus(t, pool, atMax, domain.OperationStatusFailed)
	wantOpStatus(t, pool, other, domain.OperationStatusPending)
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE type = 'operation_failed'`).
		Scan(&events); err != nil || events != 0 {
		t.Errorf("OPERATION_FAILED events = %d (%v), want 0", events, err)
	}
}

// ensureQueueBanks creates the banks with Ensure.
func ensureQueueBanks(t *testing.T, s *postgres.DB, banks ...domain.BankRef) {
	t.Helper()
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		for _, b := range banks {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}

// enqueueJob enqueues j in its own transaction and returns its id.
func enqueueJob(t *testing.T, s *postgres.DB, j store.Job) domain.OperationID {
	t.Helper()
	var id domain.OperationID
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), j)
		return err
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

// wantOpStatus fails the test unless operation id has status want (read straight from the table).
func wantOpStatus(t *testing.T, pool *pgxpool.Pool, id domain.OperationID, want domain.OperationStatus) {
	t.Helper()
	var got string
	err := pool.QueryRow(t.Context(), `SELECT status FROM operations WHERE id = $1`, string(id)).Scan(&got)
	if err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	if got != string(want) {
		t.Errorf("status of %s = %q, want %q", id, got, want)
	}
}
