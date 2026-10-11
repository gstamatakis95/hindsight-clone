package worker

// The runner and maintenance tests run against a real Postgres database (pgtest): opsDB wraps the production
// Store and Claimer, records every Fail call and reads rows back with SQL. Due-ness and heartbeat staleness
// are decided by the database clock, so the tests start the manual Clock a day in the past (retry times it
// stamps are due at once) and age heartbeats or finish times with SQL instead of advancing the clock.

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newClock returns a manual clock a day behind the database clock, at whole seconds.
func newClock() *fake.Clock {
	return fake.NewClock(time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second))
}

// failCall records one Fail.
type failCall struct {
	id      port.OperationID
	attempt int
	retryAt *time.Time
	cause   error
}

// opRow is one operations row as the tests inspect it.
type opRow struct {
	bank     port.BankRef
	kind     port.JobKind
	key      string
	status   domain.OperationStatus
	worker   string
	attempt  int
	runAt    time.Time
	progress *domain.Progress
	result   json.RawMessage
	usage    []domain.Usage
}

// opsDB is the production Store and Claimer over the test's database, recording Fail calls.
type opsDB struct {
	*postgres.Store
	t     testing.TB
	pool  *pgxpool.Pool
	mu    sync.Mutex
	fails []failCall
}

var (
	_ port.Claimer = (*opsDB)(nil)
	_ port.Store   = (*opsDB)(nil)
)

// newOps returns an opsDB over a fresh database holding bankA, bankB and bankC.
func newOps(t testing.TB) *opsDB {
	t.Helper()
	d := pgtest.New(t)
	o := &opsDB{Store: d.Store(), t: t, pool: d.Pool}
	if err := o.InTx(t.Context(), func(tx port.Tx) error {
		for _, b := range []port.BankRef{bankA, bankB, bankC} {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return o
}

// Fail records the call (with the row's attempt before it) and delegates.
func (o *opsDB) Fail(ctx context.Context, id port.OperationID, cause error, retryAt *time.Time) error {
	var attempt int
	_ = o.pool.QueryRow(ctx, `SELECT attempts FROM operations WHERE id = $1`, string(id)).Scan(&attempt)
	err := o.Store.Fail(ctx, id, cause, retryAt)
	o.mu.Lock()
	o.fails = append(o.fails, failCall{id: id, attempt: attempt, retryAt: retryAt, cause: cause})
	o.mu.Unlock()
	if err != nil {
		return fmt.Errorf("fail %s: %w", id, err)
	}
	return nil
}

// failCalls returns the recorded Fail calls.
func (o *opsDB) failCalls() []failCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.fails)
}

// add enqueues j (Payload {} when empty) in its own transaction. Safe from any goroutine: it reports
// errors with Errorf.
func (o *opsDB) add(j port.Job) port.OperationID {
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	var id port.OperationID
	ctx := context.WithoutCancel(o.t.Context())
	err := o.InTx(ctx, func(tx port.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(ctx, j)
		return err
	})
	if err != nil {
		o.t.Errorf("enqueue %s: %v", j.Kind, err)
	}
	return id
}

// row reads one operations row.
func (o *opsDB) row(id port.OperationID) opRow {
	o.t.Helper()
	var r opRow
	var tenant, bank, kind, key, status, worker string
	err := o.pool.QueryRow(o.t.Context(), `SELECT tenant_id, bank_id, kind, coalesce(serial_key, ''), status,
		coalesce(worker_id, ''), attempts, run_at FROM operations WHERE id = $1`, string(id)).
		Scan(&tenant, &bank, &kind, &key, &status, &worker, &r.attempt, &r.runAt)
	if err != nil {
		o.t.Fatalf("row %s: %v", id, err)
	}
	r.bank, r.kind, r.key = port.BankRef{Tenant: tenant, Bank: bank}, port.JobKind(kind), key
	r.status, r.worker = domain.OperationStatus(status), worker
	var op domain.Operation
	if err := o.InTx(o.t.Context(), func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(o.t.Context(), r.bank, id)
		return err
	}); err != nil {
		o.t.Fatalf("get %s: %v", id, err)
	}
	r.progress, r.result, r.usage = op.Progress, op.Result, op.Usage
	return r
}

// count counts the operations rows matching the SQL condition where (over the operations table).
func (o *opsDB) count(where string, args ...any) int {
	o.t.Helper()
	var n int
	if err := o.pool.QueryRow(o.t.Context(), `SELECT count(*) FROM operations WHERE `+where, args...).
		Scan(&n); err != nil {
		o.t.Fatalf("count %s: %v", where, err)
	}
	return n
}

// countBank counts bank b's operations of kind, with an extra SQL condition when where is not empty; its
// placeholders start at $4.
func (o *opsDB) countBank(b port.BankRef, kind port.JobKind, where string, args ...any) int {
	o.t.Helper()
	cond := `tenant_id = $1 AND bank_id = $2 AND kind = $3`
	if where != "" {
		cond += " AND " + where
	}
	return o.count(cond, append([]any{b.Tenant, b.Bank, string(kind)}, args...)...)
}

// exec runs a SQL statement on the test's database.
func (o *opsDB) exec(sql string, args ...any) {
	o.t.Helper()
	if _, err := o.pool.Exec(o.t.Context(), sql, args...); err != nil {
		o.t.Fatalf("%s: %v", sql, err)
	}
}

// ageHeartbeats moves every processing row's heartbeat d into the past (database time), as if its worker
// had stopped beating d ago.
func (o *opsDB) ageHeartbeats(d time.Duration) {
	o.t.Helper()
	o.exec(`UPDATE operations SET heartbeat_at = now() - make_interval(secs => $1) WHERE status = 'processing'`,
		d.Seconds())
}

// cancel cancels operation id of bank b in its own transaction.
func (o *opsDB) cancel(b port.BankRef, id port.OperationID) (domain.Operation, error) {
	var op domain.Operation
	ctx := o.t.Context()
	err := o.InTx(ctx, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Cancel(ctx, b, id)
		return err
	})
	if err != nil {
		return op, fmt.Errorf("cancel %s: %w", id, err)
	}
	return op, nil
}

// markDeleting tombstones bank b, creating it if needed.
func (o *opsDB) markDeleting(b port.BankRef) {
	o.t.Helper()
	o.exec(`INSERT INTO banks (tenant_id, id, deleting_at) VALUES ($1, $2, now())
		ON CONFLICT (tenant_id, id) DO UPDATE SET deleting_at = now()`, b.Tenant, b.Bank)
}

// ---------- recording store ----------

// pruneStore is a port.Store recording the cut-offs of Queue.Prune and EventRepo.Prune, optionally failing
// every InTx with txErr.
type pruneStore struct {
	port.Store
	txErr error
	mu    sync.Mutex
	ops   []time.Time
	evts  []time.Time
}

func (s *pruneStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	if s.txErr != nil {
		return s.txErr
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error { return fn(recTx{Tx: tx, s: s}) })
}

// recTx hands out recording Queue and EventRepo wrappers.
type recTx struct {
	port.Tx
	s *pruneStore
}

func (t recTx) Queue() port.Queue { return recQueue{Queue: t.Tx.Queue(), s: t.s} }

func (t recTx) Events() port.EventRepo { return recEvents{EventRepo: t.Tx.Events(), s: t.s} }

type recQueue struct {
	port.Queue
	s *pruneStore
}

func (q recQueue) Prune(ctx context.Context, before time.Time) (int, error) {
	q.s.mu.Lock()
	q.s.ops = append(q.s.ops, before)
	q.s.mu.Unlock()
	return q.Queue.Prune(ctx, before)
}

type recEvents struct {
	port.EventRepo
	s *pruneStore
}

func (e recEvents) Prune(ctx context.Context, before time.Time) (int, error) {
	e.s.mu.Lock()
	e.s.evts = append(e.s.evts, before)
	e.s.mu.Unlock()
	return e.EventRepo.Prune(ctx, before)
}

// ---------- helpers ----------

var (
	bankA = port.BankRef{Tenant: "t1", Bank: "a"}
	bankB = port.BankRef{Tenant: "t1", Bank: "b"}
	bankC = port.BankRef{Tenant: "t2", Bank: "c"}
)

// retainJob is a retain_document job for document doc of bankA.
func retainJob(doc string) port.Job {
	return port.Job{Bank: bankA, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + doc}
}

// consolidateJob is bank b's consolidate job.
func consolidateJob(b port.BankRef) port.Job {
	return port.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Dedupe: port.DedupePending,
	}
}

// eventually polls cond every few milliseconds until it holds or 10 s pass.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// startRunner runs r until the test ends (or stop is called) and returns stop, which waits for Run.
func startRunner(t *testing.T, r *Runner) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// testRunner builds a fast-polling runner over c.
func testRunner(c port.Claimer, clock port.Clock, cfg RunnerConfig) *Runner {
	cfg.Clock = clock
	if cfg.WorkerID == "" {
		cfg.WorkerID = "w1"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Millisecond
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = time.Hour
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = -1 // cancel at once, so test cleanup doesn't wait
	}
	return NewRunner(c, cfg)
}
