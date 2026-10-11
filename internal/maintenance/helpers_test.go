package maintenance

// The tick tests run against a real Postgres database (pgtest). Due-ness and heartbeat staleness are decided
// by the database clock, so the tests start the manual Clock a day in the past and age heartbeats with SQL
// instead of advancing the clock.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// quiet is a logger that drops everything, so test output stays readable.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var (
	bankA = domain.BankRef{Tenant: "t1", Bank: "a"}
	bankB = domain.BankRef{Tenant: "t1", Bank: "b"}
	bankC = domain.BankRef{Tenant: "t2", Bank: "c"}
)

// newClock returns a manual clock a day behind the database clock, at whole seconds.
func newClock() *clock.Manual {
	return clock.NewManual(time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second))
}

// opsDB is the production store and claimer over the test's database, with SQL helpers.
type opsDB struct {
	*postgres.DB
	t    testing.TB
	pool *pgxpool.Pool
}

// newOps returns an opsDB over a fresh database holding bankA, bankB and bankC.
func newOps(t testing.TB) *opsDB {
	t.Helper()
	d := pgtest.New(t)
	o := &opsDB{DB: d.Store(), t: t, pool: d.Pool}
	if err := o.InTx(t.Context(), func(tx store.Tx) error {
		for _, b := range []domain.BankRef{bankA, bankB, bankC} {
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

// add enqueues j (Payload {} when empty) in its own transaction.
func (o *opsDB) add(j store.Job) domain.OperationID {
	o.t.Helper()
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	var id domain.OperationID
	ctx := o.t.Context()
	if err := o.InTx(ctx, func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(ctx, j)
		return err
	}); err != nil {
		o.t.Fatalf("enqueue %s: %v", j.Kind, err)
	}
	return id
}

// opRow is one operations row as the tests inspect it.
type opRow struct {
	status  domain.OperationStatus
	attempt int
}

// row reads one operations row.
func (o *opsDB) row(id domain.OperationID) opRow {
	o.t.Helper()
	var r opRow
	var status string
	if err := o.pool.QueryRow(o.t.Context(), `SELECT status, attempts FROM operations WHERE id = $1`, string(id)).
		Scan(&status, &r.attempt); err != nil {
		o.t.Fatalf("row %s: %v", id, err)
	}
	r.status = domain.OperationStatus(status)
	return r
}

// count counts the operations rows matching the SQL condition where.
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
func (o *opsDB) countBank(b domain.BankRef, kind domain.OperationKind, where string, args ...any) int {
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

// ageHeartbeats moves every processing row's heartbeat d into the past (database time).
func (o *opsDB) ageHeartbeats(d time.Duration) {
	o.t.Helper()
	o.exec(`UPDATE operations SET heartbeat_at = now() - make_interval(secs => $1) WHERE status = 'processing'`,
		d.Seconds())
}

// cancel cancels operation id of bank b in its own transaction.
func (o *opsDB) cancel(b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	var op domain.Operation
	ctx := o.t.Context()
	err := o.InTx(ctx, func(tx store.Tx) error {
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
func (o *opsDB) markDeleting(b domain.BankRef) {
	o.t.Helper()
	o.exec(`INSERT INTO banks (tenant_id, id, deleting_at) VALUES ($1, $2, now())
		ON CONFLICT (tenant_id, id) DO UPDATE SET deleting_at = now()`, b.Tenant, b.Bank)
}

// retainJob is a retain_document job for document doc of bankA.
func retainJob(doc string) store.Job {
	return store.Job{Bank: bankA, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + doc}
}

// consolidateJob is bank b's automatic consolidate job.
func consolidateJob(b domain.BankRef) store.Job {
	return store.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Dedupe: store.DedupePending,
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

// pruneStore is a store.DB recording the cut-offs of Queue.Prune and EventRepo.Prune.
type pruneStore struct {
	store.DB
	mu   sync.Mutex
	ops  []time.Time
	evts []time.Time
}

func (s *pruneStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error { return fn(recTx{Tx: tx, s: s}) })
}

// recTx hands out recording Queue and EventRepo wrappers.
type recTx struct {
	store.Tx
	s *pruneStore
}

func (t recTx) Queue() store.Queue { return recQueue{Queue: t.Tx.Queue(), s: t.s} }

func (t recTx) Events() store.EventRepo { return recEvents{EventRepo: t.Tx.Events(), s: t.s} }

type recQueue struct {
	store.Queue
	s *pruneStore
}

func (q recQueue) Prune(ctx context.Context, before time.Time) (int, error) {
	q.s.mu.Lock()
	q.s.ops = append(q.s.ops, before)
	q.s.mu.Unlock()
	return q.Queue.Prune(ctx, before)
}

type recEvents struct {
	store.EventRepo
	s *pruneStore
}

func (e recEvents) Prune(ctx context.Context, before time.Time) (int, error) {
	e.s.mu.Lock()
	e.s.evts = append(e.s.evts, before)
	e.s.mu.Unlock()
	return e.EventRepo.Prune(ctx, before)
}
