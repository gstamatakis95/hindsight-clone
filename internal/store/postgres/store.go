package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the Postgres implementation of store.DB (with its Tx, Reader and repositories), store.Maintenance
// and jobs.Claimer (satisfied structurally; the assertion sits in a test file, so the adapter never imports
// jobs). It is safe for concurrent use.
type DB struct {
	pool   *pgxpool.Pool
	reader *reader
}

var (
	_ store.DB     = (*DB)(nil)
	_ store.Tx     = (*tx)(nil)
	_ store.Reader = (*reader)(nil)
)

// New returns a DB over pool. The caller owns the pool and closes it after the DB is no longer used.
// The pool must allow at least two connections: Read() never waits on an open transaction.
func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool, reader: &reader{conn: pool, pool: pool}}
}

// Read returns the long-lived read-only view over the pool; each call is its own statement.
func (s *DB) Read() store.Reader { return s.reader }

// txAttempts bounds how often InTx runs a transaction that loses a deadlock or serialization race.
const txAttempts = 3

// InTx runs fn in one READ COMMITTED transaction and commits when fn returns nil. Any error or panic
// rolls back; a panic is re-raised after the rollback. fn's error is returned unchanged. A transaction that
// fails with a deadlock (40P01), a serialization failure (40001) or errEntityGone is rolled
// back and run again, up to txAttempts times in all: fn never calls a model (store.DB), so re-running it
// only repeats short database work.
func (s *DB) InTx(ctx context.Context, fn func(store.Tx) error) error {
	var err error
	for range txAttempts {
		if err = s.runTx(ctx, fn); !retryableTxErr(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// retryableTxErr reports whether err is a lost deadlock or serialization race that a fresh transaction
// may win, or an Attach of an entity pruned meanwhile (errEntityGone), which a re-run resolves afresh.
func retryableTxErr(err error) bool {
	return isPgCode(err, codeDeadlockDetected) || isPgCode(err, codeSerializationFailure) ||
		errors.Is(err, errEntityGone)
}

// runTx runs fn in one transaction (see InTx).
func (s *DB) runTx(ctx context.Context, fn func(store.Tx) error) error {
	pgTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return mapErr("begin", err)
	}
	done := false
	defer func() {
		if !done {
			_ = pgTx.Rollback(context.WithoutCancel(ctx)) // the panic or fn's error is what matters
		}
	}()
	t := newTx(pgTx)
	if err := fn(t); err != nil {
		return err
	}
	done = true
	if err := pgTx.Commit(ctx); err != nil {
		return mapErr("commit", err)
	}
	return nil
}

// tx is one Postgres transaction. It is used by one goroutine at a time, like the pgx.Tx it wraps.
type tx struct {
	*reader
	pgTx pgx.Tx
	q    *db.Queries
	seqs map[domain.BankRef]int64
}

// newTx wraps a pgx transaction.
func newTx(pgTx pgx.Tx) *tx {
	return &tx{
		reader: &reader{conn: pgTx},
		pgTx:   pgTx,
		q:      db.New(pgTx),
		seqs:   map[domain.BankRef]int64{},
	}
}

// Seq bumps the bank's change_seq once per transaction and returns it; the bank row stays locked until
// commit, so within a bank seq order is commit order. It also stamps last_memory_write_at.
func (t *tx) Seq(ctx context.Context, b domain.BankRef) (int64, error) {
	if seq, ok := t.seqs[b]; ok {
		return seq, nil
	}
	seq, err := t.q.BumpBankSeq(ctx, db.BumpBankSeqParams{TenantID: b.Tenant, ID: b.Bank})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, t.missingOrDeleting(ctx, b)
	}
	if err != nil {
		return 0, mapErr("seq", err)
	}
	t.seqs[b] = seq
	return seq, nil
}

// missingOrDeleting explains why a live-bank update matched no row: domain.ErrBankDeleting for a
// tombstoned bank, domain.ErrNotFound for an unknown one.
func (t *tx) missingOrDeleting(ctx context.Context, b domain.BankRef) error {
	bank, err := t.q.GetBank(ctx, db.GetBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return bankErr(b, err)
	}
	if bank.DeletingAt != nil {
		return fmt.Errorf("%w: %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	return fmt.Errorf("%w: bank %s/%s", domain.ErrConflict, b.Tenant, b.Bank)
}

// Banks returns the bank repository bound to this transaction.
func (t *tx) Banks() store.BankRepo { return &bankRepo{q: t.q, tx: t} }

// Documents returns the document repository bound to this transaction.
func (t *tx) Documents() store.DocumentRepo { return &documentRepo{q: t.q, tx: t} }

// Chunks returns the chunk repository bound to this transaction.
func (t *tx) Chunks() store.ChunkRepo { return &chunkRepo{q: t.q, tx: t} }

// Units returns the memory-unit repository bound to this transaction.
func (t *tx) Units() store.UnitRepo { return &unitRepo{q: t.q, tx: t} }

// Entities returns the entity repository bound to this transaction.
func (t *tx) Entities() store.EntityRepo { return &entityRepo{q: t.q, tx: t} }

// Evidence returns the observation-evidence repository bound to this transaction.
func (t *tx) Evidence() store.EvidenceRepo { return &evidenceRepo{q: t.q, tx: t} }

// MentalModels returns the mental-model repository bound to this transaction.
func (t *tx) MentalModels() store.MentalModelRepo { return &mentalModelRepo{q: t.q, tx: t} }

// Directives returns the directive repository bound to this transaction.
func (t *tx) Directives() store.DirectiveRepo { return &directiveRepo{q: t.q, tx: t} }

// Queue returns the operations table seen from this transaction.
func (t *tx) Queue() store.Queue { return &queue{q: t.q, tx: t} }

// Events returns the lifecycle-event repository bound to this transaction.
func (t *tx) Events() store.EventRepo { return &eventRepo{q: t.q, tx: t} }
