package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Sizes of the serial-key acceptance test.
const (
	serialJobs     = 500
	serialKeys     = 20
	serialClaimers = 8
	serialBatch    = 3
	serialDeadline = 2 * time.Minute
)

// keyHolders tracks which job holds each serial key while the test's workers run it, and the enqueue
// index of the last job run per key.
type keyHolders struct {
	mu         sync.Mutex
	held       map[string]port.OperationID
	last       map[string]int
	violations []string
}

// take records j as its key's holder, noting a violation if another job already holds the key or if j was
// enqueued before the key's previous job (per-key FIFO).
func (h *keyHolders) take(j port.Job) {
	var p struct{ I int }
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		p.I = -1
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.held[j.SerialKey]; ok {
		h.violations = append(h.violations, fmt.Sprintf("key %q held by %s and %s", j.SerialKey, prev, j.ID))
	}
	if prev, ok := h.last[j.SerialKey]; ok && p.I <= prev {
		h.violations = append(h.violations, fmt.Sprintf("key %q ran job %d after job %d (not FIFO)", j.SerialKey,
			p.I, prev))
	}
	h.held[j.SerialKey], h.last[j.SerialKey] = j.ID, p.I
}

// release clears j's key; called before Complete commits, so the next job of the key can't race it.
func (h *keyHolders) release(j port.Job) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.held, j.SerialKey)
}

// TestAcceptSerialKeys is the "serial keys" acceptance test: 8 concurrent claimers work through 500
// jobs spread over 20 serial keys, holding each job 1-5 ms. No key is ever held by two jobs at once (checked by
// the workers and by sampling the table), each key's jobs run in enqueue order, Claim never errors and every
// job completes.
func TestAcceptSerialKeys(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	bank := port.BankRef{Tenant: "tenant-serial", Bank: "bank-serial"}
	ensureQueueBanks(t, s, bank)
	enqueueSerialJobs(t, s, bank)

	ctx, cancel := context.WithTimeout(t.Context(), serialDeadline)
	defer cancel()
	holders := &keyHolders{held: map[string]port.OperationID{}, last: map[string]int{}}
	var done, claimErrs atomic.Int64
	stop := make(chan struct{})
	sampled := make(chan sampleResult, 1)
	go func() { sampled <- sampleDoubleHeld(ctx, pool, stop) }()

	var wg sync.WaitGroup
	for w := range serialClaimers {
		wg.Go(func() { serialWorker(ctx, t, s, fmt.Sprintf("worker-%d", w), holders, &done, &claimErrs) })
	}
	wg.Wait()
	close(stop)
	res := <-sampled

	if n := claimErrs.Load(); n != 0 {
		t.Errorf("Claim returned %d errors, want 0", n)
	}
	if n := done.Load(); n != serialJobs {
		t.Errorf("completed %d jobs, want %d", n, serialJobs)
	}
	for _, v := range holders.violations {
		t.Errorf("serial key violation: %s", v)
	}
	if res.err != nil {
		t.Errorf("sampler: %v", res.err)
	}
	if res.maxDup != 0 || res.samples == 0 {
		t.Errorf("sampler saw %d keys processing twice over %d samples, want 0 over > 0", res.maxDup, res.samples)
	}
	var left int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM operations WHERE status <> 'completed'`).
		Scan(&left); err != nil || left != 0 {
		t.Errorf("operations not completed = %d (%v), want 0", left, err)
	}
}

// enqueueSerialJobs enqueues serialJobs retain_document jobs with DedupeNone, round-robin over serialKeys
// keys, in batches of 50 per transaction.
func enqueueSerialJobs(t *testing.T, s *postgres.Store, bank port.BankRef) {
	t.Helper()
	for start := 0; start < serialJobs; start += 50 {
		if err := s.InTx(t.Context(), func(tx port.Tx) error {
			for i := start; i < min(start+50, serialJobs); i++ {
				j := port.Job{
					Bank: bank, Kind: domain.OperationKindRetainDocument, SerialKey: fmt.Sprintf("doc:%d", i%serialKeys),
					Payload: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
				}
				if _, _, err := tx.Queue().Enqueue(t.Context(), j); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
}

// serialWorker claims and completes jobs until every job is done or ctx ends.
func serialWorker(
	ctx context.Context, t *testing.T, s *postgres.Store, worker string, h *keyHolders, done, claimErrs *atomic.Int64,
) {
	kinds := []port.JobKind{domain.OperationKindRetainDocument}
	for done.Load() < serialJobs && ctx.Err() == nil {
		js, err := s.Claim(ctx, worker, kinds, serialBatch)
		if err != nil {
			if ctx.Err() == nil {
				claimErrs.Add(1)
				t.Logf("Claim(%s): %v", worker, err)
			}
			continue
		}
		if len(js) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		for _, j := range js {
			h.take(j)
		}
		time.Sleep(time.Duration(1000+rand.IntN(4000)) * time.Microsecond) //nolint:gosec // test jitter
		for _, j := range js {
			h.release(j)
			if err := s.Complete(ctx, j.ID, json.RawMessage(`{}`), nil); err != nil {
				t.Errorf("Complete(%s): %v", j.ID, err)
				continue
			}
			done.Add(1)
		}
	}
}

// sampleResult is what sampleDoubleHeld saw.
type sampleResult struct {
	samples int
	maxDup  int
	err     error
}

// sampleDoubleHeld repeatedly counts serial keys with more than one PROCESSING row until stop closes.
func sampleDoubleHeld(ctx context.Context, pool *pgxpool.Pool, stop <-chan struct{}) sampleResult {
	var res sampleResult
	for {
		select {
		case <-stop:
			return res
		default:
		}
		var dup int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT 1 FROM operations WHERE status = 'processing' AND serial_key IS NOT NULL
			GROUP BY tenant_id, bank_id, serial_key HAVING count(*) > 1) d`).Scan(&dup)
		if err != nil {
			if ctx.Err() == nil {
				res.err = err
			}
			return res
		}
		res.samples++
		res.maxDup = max(res.maxDup, dup)
	}
}

// TestAcceptReclaim is the "reclaim" acceptance test: a claimed job whose heartbeat goes stale is
// reclaimed to PENDING with attempt+1 and is claimable again; at max attempts it becomes FAILED with exactly
// one operation_failed event, and the stale worker's late Complete is fenced.
func TestAcceptReclaim(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	bank := port.BankRef{Tenant: "tenant-reclaim", Bank: "bank-reclaim"}
	ensureQueueBanks(t, s, bank)
	kind := domain.OperationKindRetainDocument
	id := enqueueJob(t, s, port.Job{Bank: bank, Kind: kind, SerialKey: "doc:1", Payload: json.RawMessage(`{}`)})
	maxAttempts := kind.MaxAttempts()
	for round := range maxAttempts {
		js, err := s.Claim(ctx, "worker", []port.JobKind{kind}, 1)
		if err != nil || len(js) != 1 || js[0].ID != id || js[0].Attempt != round {
			t.Fatalf("round %d: Claim = %+v, %v; want %s at attempt %d", round, js, err, id, round)
		}
		time.Sleep(400 * time.Millisecond)
		if n, err := s.ReclaimStale(ctx, 200*time.Millisecond); err != nil || n != 1 {
			t.Fatalf("round %d: ReclaimStale = %d, %v; want 1, nil", round, n, err)
		}
		if err := s.Complete(ctx, id, json.RawMessage(`{}`), nil); err == nil {
			t.Fatalf("round %d: the stale worker's Complete succeeded after the reclaim", round)
		}
		want := domain.OperationStatusPending
		if round == maxAttempts-1 {
			want = domain.OperationStatusFailed
		}
		wantOpStatus(t, pool, id, want)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE type = 'operation_failed'
		AND operation_id = $1 AND tenant_id = $2 AND bank_id = $3`, string(id), bank.Tenant, bank.Bank).
		Scan(&events); err != nil || events != 1 {
		t.Errorf("operation_failed events = %d (%v), want 1", events, err)
	}
	if n, err := s.ReclaimStale(ctx, 200*time.Millisecond); err != nil || n != 0 {
		t.Errorf("ReclaimStale after the job failed = %d, %v; want 0, nil", n, err)
	}
}
