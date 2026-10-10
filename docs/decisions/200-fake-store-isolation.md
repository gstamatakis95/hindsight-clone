# 200 — Isolation model of the in-memory fake store (WP-02)

## Context
The card asks for an in-memory Store/Tx "with Seq and real isolation semantics (a mutex per bank is
fine)". The porttest suites need: rollback of every write (job rows and the Seq bump included), a Read()
that never blocks on an open InTx and sees only committed data, a second Seq on the same bank that blocks
while the first transaction is open, and safe concurrent claims, enqueues and upgrades. Claim, Heartbeat,
ReclaimStale and the operations table cut across banks, which a per-bank mutex would have to coordinate.

## Decision
- One store-wide write lock (a one-slot channel, so waiting honours ctx). Each write transaction clones
  the committed state, works on the clone and publishes it with an atomic pointer swap on commit; an
  error or panic discards it. Transactions are therefore serializable, which is stronger than READ
  COMMITTED but never weaker than what engines may rely on.
- Tables are flat maps (or slices) of value types; nested slices, maps and pointers are treated as
  immutable and deep-copied at the API boundary, so a shallow clone per table is a real copy and a
  published snapshot is never written again. Read() and Claimer.Alive load the snapshot without the lock.
- Every other Claimer method is its own transaction on the same lock. Complete/Fail of a cancel-requested
  job commit the CANCELLED status and then return domain.ErrCancelled.
- Postgres's per-transaction now(): the Clock is read once per transaction.

## Consequence
An InTx callback must not open another InTx or call a Claimer write; that blocks until ctx ends. This is
documented in the package doc. Engines don't nest transactions (AGENTS.md), so it only bites a test that
would also misbehave on Postgres row locks.

## Alternatives
- A mutex per bank with merge-on-commit: needs cross-bank coordination for the queue and the claimer and
  buys concurrency the tests don't need.
- An undo log instead of a clone: more code for no observable difference at test sizes.

## Spec lines involved
Ports (Store.InTx, Tx.Seq, Reader), Jobs (claim SQL, serial keys), WP-02 card, decision 009.
