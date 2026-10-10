package fake

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Store is an in-memory port.Store and port.Claimer: the whole Postgres schema (banks, units, archive,
// documents, chunks, entities, links, evidence, events, mental models, directives and the operations
// queue) held in maps. It passes the same porttest suites as the Postgres adapter.
//
// Isolation: write transactions run one at a time (one store-wide lock, so they are serializable) on a
// private copy of the data; commit publishes the copy atomically and an error or panic discards it.
// Read() and Claimer.Alive never take the lock: they see the latest committed snapshot and never block
// on an open InTx, exactly like the Postgres Reader. Every other Claimer method is its own short
// transaction on the same lock.
//
// Because transactions are serialized, an InTx callback must never start another InTx or call a Claimer
// method other than Alive (that would deadlock until ctx is done); Read() is safe anywhere.
//
// Time: every timestamp the store writes (created_at, run_at defaults, heartbeats, finished_at, event
// times) and every due-ness or staleness check comes from the Clock given to NewStore, read once per
// transaction like Postgres's now(). Engine tests pass a *Clock and Advance it to make jobs due or
// heartbeats stale.
//
// Ids: units, entities, chunks, directives and operations get random UUIDs when the caller leaves the id
// empty. Ties in "oldest first" orderings are broken by insertion order, so a frozen clock still gives
// deterministic results.
type Store struct {
	// lock is a one-slot semaphore held by the running write transaction.
	lock chan struct{}
	// cur is the latest committed snapshot.
	cur atomic.Pointer[state]
	// clock supplies the transaction time.
	clock port.Clock
}

var (
	_ port.Store   = (*Store)(nil)
	_ port.Claimer = (*Store)(nil)
)

// NewStore returns an empty store (no banks, tenants, units or jobs) whose time comes from clock. Pass a
// *Clock in engine tests; the porttest factories pass a wall clock.
func NewStore(clock port.Clock) *Store {
	s := &Store{lock: make(chan struct{}, 1), clock: clock}
	s.cur.Store(newState())
	return s
}

// InTx runs fn in one transaction on a private copy of the data and publishes the copy when fn returns
// nil. An error from fn is returned unchanged (errors.Is matches it) and discards every write, the job
// rows and the Seq bump included; a panic discards them too and propagates. Waiting for the store lock
// honours ctx.
func (s *Store) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.write(ctx, func(t *tx) error { return fn(t) })
}

// Read returns a Reader over the latest committed snapshot. Each call of a Reader method sees every
// transaction committed before it; it never blocks.
func (s *Store) Read() port.Reader { return reader{s: s} }

// write runs fn as one serialized transaction and commits its copy when fn returns nil.
func (s *Store) write(ctx context.Context, fn func(*tx) error) error {
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%w: fake store: %w", domain.ErrUnavailable, ctx.Err())
	}
	defer func() { <-s.lock }()
	t := &tx{st: s.cur.Load().clone(), now: s.clock.Now(), seqs: map[port.BankRef]int64{}}
	// A Tx used after its callback returned would write into the published snapshot; make it panic instead.
	defer func() { t.st = nil }()
	if err := fn(t); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil { // like a Postgres tx whose context ended before COMMIT
		return fmt.Errorf("%w: fake store: not committed: %w", domain.ErrUnavailable, err)
	}
	s.cur.Store(t.st)
	return t.after
}

// snapshot returns the latest committed state; callers must not modify it.
func (s *Store) snapshot() *state { return s.cur.Load() }

// newUUID returns a random UUID string for a generated id.
func newUUID() string { return uuid.NewString() }

// tx is one transaction: a private copy of the state, the transaction time and the Seq values taken.
type tx struct {
	st   *state
	now  time.Time
	seqs map[port.BankRef]int64
	// after is returned by an internal write after it commits (Complete or Fail of a cancelled job).
	after error
}

// commitWith makes the internal write commit and then return err.
func (t *tx) commitWith(err error) error {
	t.after = err
	return nil
}

var _ port.Tx = (*tx)(nil)

// Seq bumps the bank's change_seq once per transaction and returns it; later calls return the same value.
func (t *tx) Seq(ctx context.Context, b port.BankRef) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, ctxErr(err)
	}
	return t.seq(b)
}

// seq implements Seq without a context.
func (t *tx) seq(b port.BankRef) (int64, error) {
	if v, ok := t.seqs[b]; ok {
		return v, nil
	}
	row, ok := t.st.banks[b]
	if !ok {
		return 0, notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	if row.bank.Deleting() {
		return 0, fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	row.bank.ChangeSeq++
	row.bank.LastMemoryWriteAt = ptrCopy(&t.now)
	row.bank.UpdatedAt = t.now
	t.st.banks[b] = row
	t.seqs[b] = row.bank.ChangeSeq
	return row.bank.ChangeSeq, nil
}

// Banks returns the bank repository of this transaction.
func (t *tx) Banks() port.BankRepo { return bankRepo{t} }

// Documents returns the document repository of this transaction.
func (t *tx) Documents() port.DocumentRepo { return docRepo{t} }

// Chunks returns the chunk repository of this transaction.
func (t *tx) Chunks() port.ChunkRepo { return chunkRepo{t} }

// Units returns the memory-unit repository of this transaction.
func (t *tx) Units() port.UnitRepo { return unitRepo{t} }

// Entities returns the entity repository of this transaction.
func (t *tx) Entities() port.EntityRepo { return entityRepo{t} }

// Links returns the link repository of this transaction.
func (t *tx) Links() port.LinkRepo { return linkRepo{t} }

// Evidence returns the observation-evidence repository of this transaction.
func (t *tx) Evidence() port.EvidenceRepo { return evidenceRepo{t} }

// MentalModels returns the mental-model repository of this transaction.
func (t *tx) MentalModels() port.MentalModelRepo { return modelRepo{t} }

// Directives returns the directive repository of this transaction.
func (t *tx) Directives() port.DirectiveRepo { return directiveRepo{t} }

// Queue returns the operations table of this transaction.
func (t *tx) Queue() port.Queue { return queue{t} }

// Events returns the lifecycle-event repository of this transaction.
func (t *tx) Events() port.EventRepo { return eventRepo{t} }

// SearchVector runs the semantic arm over this transaction's view (its own writes included).
func (t *tx) SearchVector(ctx context.Context, q port.VectorQuery) ([]port.Hit, error) {
	return searchVector(ctx, t.st, q)
}

// SearchText runs the keyword arm over this transaction's view.
func (t *tx) SearchText(ctx context.Context, q port.TextQuery) ([]port.Hit, error) {
	return searchText(ctx, t.st, q)
}

// SearchTemporal runs the temporal arm over this transaction's view.
func (t *tx) SearchTemporal(ctx context.Context, q port.TemporalQuery) ([]port.Hit, error) {
	return searchTemporal(ctx, t.st, q)
}

// ExpandGraph runs the graph arm over this transaction's view.
func (t *tx) ExpandGraph(ctx context.Context, q port.GraphQuery) ([]port.Hit, error) {
	return expandGraph(ctx, t.st, q)
}

// GetUnits returns live units of b in the order of ids from this transaction's view.
func (t *tx) GetUnits(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]domain.Unit, error) {
	return getUnits(ctx, t.st, b, ids)
}

// BankSeq returns the bank's change_seq as this transaction sees it.
func (t *tx) BankSeq(ctx context.Context, b port.BankRef) (int64, error) {
	return bankSeq(ctx, t.st, b)
}

// reader is the lock-free Reader over the latest committed snapshot.
type reader struct {
	s *Store
}

var _ port.Reader = reader{}

// SearchVector is the semantic arm: cosine similarity, nearest first.
func (r reader) SearchVector(ctx context.Context, q port.VectorQuery) ([]port.Hit, error) {
	return searchVector(ctx, r.s.snapshot(), q)
}

// SearchText is the keyword arm: BM25 over unit text.
func (r reader) SearchText(ctx context.Context, q port.TextQuery) ([]port.Hit, error) {
	return searchText(ctx, r.s.snapshot(), q)
}

// SearchTemporal is the temporal arm: units whose occurrence overlaps the window.
func (r reader) SearchTemporal(ctx context.Context, q port.TemporalQuery) ([]port.Hit, error) {
	return searchTemporal(ctx, r.s.snapshot(), q)
}

// ExpandGraph is the graph arm: outgoing links from the seeds, up to MaxHops.
func (r reader) ExpandGraph(ctx context.Context, q port.GraphQuery) ([]port.Hit, error) {
	return expandGraph(ctx, r.s.snapshot(), q)
}

// GetUnits returns the live units with the given ids in the order of ids.
func (r reader) GetUnits(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]domain.Unit, error) {
	return getUnits(ctx, r.s.snapshot(), b, ids)
}

// BankSeq returns the bank's latest committed change_seq.
func (r reader) BankSeq(ctx context.Context, b port.BankRef) (int64, error) {
	return bankSeq(ctx, r.s.snapshot(), b)
}

// bankSeq reads a bank's change_seq from st.
func bankSeq(ctx context.Context, st *state, b port.BankRef) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, ctxErr(err)
	}
	row, ok := st.banks[b]
	if !ok {
		return 0, notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	return row.bank.ChangeSeq, nil
}

// ---------- errors ----------

// notFound returns an error wrapping domain.ErrNotFound.
func notFound(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrNotFound, fmt.Sprintf(format, args...))
}

// precondition returns an error wrapping domain.ErrFailedPrecondition.
func precondition(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrFailedPrecondition, fmt.Sprintf(format, args...))
}

// ctxErr wraps a context error.
func ctxErr(err error) error {
	return fmt.Errorf("fake store: %w", err)
}
