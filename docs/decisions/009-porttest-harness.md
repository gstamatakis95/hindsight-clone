# 009: Shape of the porttest contract harness

Status: accepted (WP-00)

## Context

SPEC.md requires one shared contract suite per port. The Postgres adapter runs it against a real database
(testcontainers), and the in-memory fake must pass it too, "so fakes can't drift". The spec doesn't say how a
suite is handed an implementation, how state is isolated between tests, or how tests that depend on time and
concurrency stay reliable on both a real Postgres and a fake. Several properties make this harder:

- Claim, ReclaimStale and Heartbeat act on the whole operations table across banks, not on one bank.
- `Tx.Seq` blocks on a row lock, and Postgres's `now()` is fixed for the length of a transaction.
- A unique violation aborts the Postgres transaction it happens in.
- The test process and the database (a Docker VM on macOS) may not agree on the time.

## Decision

- **Factories, one per subtest.** The suites take these factory types:
  - `StoreFactory func(*testing.T) port.Store`
  - `QueueFactory func(*testing.T) (port.Store, port.Claimer)`: the Claimer works over the same database as
    the Store.
  - `BlobFactory func(*testing.T) port.Blob`

  `RunStoreSuite`, `RunSeqSuite`, `RunQueueSuite` and `RunBlobSuite` each run named `t.Run` subtests and call
  the factory once per subtest. Adapters call them from their own `_test.go`. The factory gets the subtest's
  `t`, so cleanup goes through `t.Cleanup`.
- **Every factory call returns an empty database or bucket.** The Postgres harness reuses one container and
  truncates every table on each call; the fakes build a fresh instance. Because the database starts empty,
  fixed bank names (`tenant-1/bank-a`, ...) are safe, and claim assertions can be exact sets.
- **No `t.Parallel`.** Claim and ReclaimStale are global, so two subtests sharing a database would steal each
  other's jobs. Tests that need concurrency start goroutines inside one subtest:
  - Seq: tx B must stay blocked for 150 ms while tx A holds the bank row.
  - Claim: four concurrent claimers, followed by one sweep claim. Any Claim error fails the test: a lost
    serial-key race is retried inside Claim and never surfaces as `ErrConflict`.
  - Dedupe: eight concurrent `DedupePending` enqueuers of one key end with exactly one row (one insert, every
    caller gets its id).
  - Upgrade: two concurrent joiners each join and upgrade one row in their own transaction; both payload keys
    survive and booleans are OR-ed. Goroutines never wait on each other inside a transaction, because the
    dedupe advisory lock serializes the joiners until commit.

  Stores must therefore serve at least two connections, and `Read()` must not block on an open `InTx`. While
  a transaction holds a bank's Seq S, `Read().BankSeq` must return S-1 promptly and `Read().GetUnits` must not
  see that transaction's units. A Reader obtained once must see later commits on its next call.
- **Real time with wide margins.**
  - Due-ness (`run_at`) is decided by the database clock. Tests use `now ± 1 h`, never a boundary close to
    now. `Fail` with a retry time in the past is claimable at once; one in the future is not.
  - Heartbeat staleness compares database time with database time. Tests sleep 400 ms against a `ReclaimStale`
    ttl of 200 ms. The heartbeat tests sleep first, then heartbeat, then expect `ReclaimStale` to touch only
    the rows that were not refreshed, so a count of 0 (or 1) proves the refresh happened.
  - `Queue.Prune` and `EventRepo.Prune` are checked with cutoffs an hour in the past (deletes nothing) and an
    hour in the future (deletes every eligible row). Events are appended with `At = now`; whether an adapter
    stores `e.At` or its own `now()` is not settled, and both readings pass.
  - Fixed timestamps are whole seconds in UTC, so they round-trip through microsecond storage exactly.
  - Jobs whose relative age matters are enqueued in separate transactions.
- **Transaction hygiene.**
  - An `InTx` callback never calls `t.Fatal`; it returns an error, and the test asserts after `InTx`
    returns.
  - Each expected error (`ErrAlreadyExists`, `ErrNotFound`, `ErrFailedPrecondition`, `ErrBankDeleting`) gets
    its own transaction and is matched with `errors.Is`. `InTx` must therefore return the callback's error
    unchanged or wrapped.
- **Reference semantics instead of hand-written expectations** wherever the port defines them:
  - Tag filters and seq ranges: the expected hits are computed with `(*port.SQLPredicate).Eval` and
    `port.SeqRange.Contains` over the inserted units. One fixture (five tagged units, each in its own
    transaction, each a hit of every arm) runs every predicate and every kind of seq range (zero, bounded,
    lower-only, upper-only, `UpTo` = 0, past the end) through all four arms. Only the vector arm's order is
    asserted.
  - Operation state is asserted through `Queue.Get` and `Queue.List` (status, attempts, error, result, usage,
    progress, worker), not inferred from `Alive` or later claims.
  - JSON payloads and configs are compared after decoding, so jsonb key reordering doesn't matter.
  - Embeddings are `(cos θ, sin θ, 0, ...)` unit vectors, so each score must equal `cos θ` (within 1e-3) and
    the ordering is known in advance.
- **Ambiguous contracts are tested only where every reasonable reading agrees.** Each such point is listed in
  the WP-00 report for a CCR. Examples:
  - ExpandGraph uses forward edges only.
  - DeleteCiting's reset set must be exactly the observation's other sources, excluding the cited ids (the
    plain reading of "other sources"); only those reset sources must reappear in Unconsolidated.
  - The restored unit's embedding is not asserted.
  - Restore is checked to reattach outgoing causal links (ExpandGraph from the restored unit) and entities
    (`PruneOrphans` keeps them). Restoring incoming links, and entity co-occurrence counts while the unit is
    archived, are not asserted.
  - Graph-dirty and entity-maintenance queues are cleared to a baseline (`ClearDirty`, `PruneOrphans` of
    referenced entities) before the delete or archive under test, so it doesn't matter whether `Insert` or
    `Attach` also queue rows.
  - `DeleteBatch` runs on a bank that is not tombstoned, so the follow-up reads (documents, models,
    directives, chunks) do not depend on how repositories treat a deleting bank.
  - `RecordObservationVersion` and `AppendAudit` have no read port yet; they are only checked to be accepted.
  - `Retry` of a row cancelled while processing but not yet finished is not tested.
- **Fencing and cancel assumptions** (review round 1, R3, R7, R8):
  - `Progress`, `Complete` and `Fail` on a row that is not PROCESSING return `ErrFailedPrecondition` and change
    nothing. This covers a reclaimed row whose stale worker reports late, a completed row, and a row cancelled
    while pending (finished at once). An unknown id returns `ErrNotFound`. The only exception is a row
    cancelled while processing and not yet finished: `Complete` and `Fail` finish it, keep it CANCELLED and
    return `ErrCancelled`.
  - `Reschedule` and `Upgrade` act on PENDING rows only.
  - A row cancelled while processing keeps its serial key until it is finished. `Heartbeat` keeps such a row
    fresh but still reports it in `*LostJobsError`. `ReclaimStale` finishes the row once its heartbeat is
    stale. `Alive` is true only for PROCESSING rows, so a parked, retried or reclaimed pending row is not
    alive.
- **Placeholders name the pending work package.** `porttest_test.go` has one test per (suite, implementation)
  pair. Each only calls `t.Skip`, naming the WP that wires it:
  - WP-01: Postgres adapter, testcontainers.
  - WP-02: fakes.
  - WP-03: S3 adapter on MinIO.

  `go test -v` therefore lists what is still unwired. The same file holds compile-time guards on the `Run*`
  and factory signatures, plus self-tests of the helpers (UUID format, vector geometry, predicate fixtures).
- **Files.** The spec's layout lists `doc.go store_suite.go blob_suite.go queue_suite.go seq_suite.go`. We add:
  - `harness.go`: factories and helpers.
  - `repo_suite.go`, `bank_suite.go`, `curation_suite.go` and `search_suite.go`: further cases of
    `RunStoreSuite`, split out for length. The last two hold curation and maintenance writes, and the
    four-arm filters, Reader reuse and tenant isolation.
  - `queue_worker_suite.go` and `queue_control_suite.go`: further cases of `RunQueueSuite`. The first holds
    the Claimer cases (heartbeats, reclaim, fencing, terminal transitions, concurrent claims); the second
    holds operation control (Get, List, Cancel, Retry, Prune, CancelBank, deleting and deleted banks,
    tenant isolation).

  None of these files is a separate suite.

## Alternatives

- **A `Harness` interface** (`NewStore`, `Reset`, `Close`) instead of factory functions. Rejected: it adds a
  type for each adapter to implement and gains nothing over a closure plus `t.Cleanup`.
- **One database per suite run with unique bank names per subtest.** Rejected: Claim and ReclaimStale are
  global, so leftover jobs from earlier subtests would make claim assertions order-dependent.
- **Parallel subtests on separate schemas or containers.** Rejected for v1: a container per subtest costs too
  much time, and per-schema isolation would leak into the adapter's connection setup.
- **An injected clock in the queue.** Rejected: the claim SQL uses Postgres `now()` by design (SPEC "Jobs"),
  and faking it would test a different query than production runs.

## Spec lines involved

- File structure (porttest layout, line ~1004).
- "Every port has one shared contract test suite" (~1029), "go test -race" (~1087).
- Ports (~1089-1314).
- Jobs: claim SQL and the unique partial index (~1928-1945), one-at-a-time per key, heartbeats and reclaim,
  retries (~1947-1955).
- Curation (~1957-1971).
- Behavioural rules: DeleteBank (~1980) and the change sequence (~1984).
