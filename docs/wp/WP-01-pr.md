# WP-01 · Postgres adapter

Branch `wp/WP-01-postgres`, rebased on main. Owns `internal/adapter/postgres/**` (plus two new porttest files,
allowed by the orchestrator's ruling). Plan: `docs/wp/WP-01.md`.

## What was built

- [x] `Store`, `Tx` and `Seq` (`store.go`): `InTx` at READ COMMITTED (rolls back on error and panic, never
      re-runs `fn`); `Seq` = `UPDATE banks SET change_seq = change_seq + 1 ... WHERE deleting_at IS NULL
      RETURNING change_seq`, cached so it runs at most once per tx per bank, with the row lock held to
      commit; tombstoned bank `ErrBankDeleting`, unknown bank `ErrNotFound`.
- [x] Every repo as sqlc queries (`queries/*.sql`, one file per area; generated `db/`): banks (+ tenant
      config, DeleteBatch over every bank-keyed table, Delete removing the bank row first), documents,
      chunks, units (insert, observation upsert/versions, update, retag, audit, unconsolidated, mark,
      delete-by-document, archive/restore with entity and causal-link snapshots), entities (trigram
      candidates, co-occurrence, label upsert, attach/detach, maintenance, prune), links (+ graph_dirty),
      evidence (cites, delete-citing with co-source reset), mental models (+ capped history), directives,
      lifecycle events (seq-group-safe paging, filtered lastSeq, prune). The tag predicate and the search
      arms are dynamic SQL with bind parameters only (the predicate is a variable tree).
- [x] `Reader`:
  - [x] `SearchVector`: per query, `set_config` (SET LOCAL) of `hnsw.ef_search = 200`,
        `hnsw.iterative_scan = strict_order`, `hnsw.max_scan_tuples = 100000`; index-friendly inner
        `ORDER BY embedding <=> q LIMIT n`, similarity floor outside.
  - [x] `SearchText`: pg_search BM25 (`|||` on text and context, `paradedb.score`).
  - [x] `SearchTemporal`: span overlap with a half-open window, proximity score, round-robin buckets.
  - [x] `ExpandGraph`: recursive CTE capped by hops (forward edges, best path-weight product).
  - [x] `GetUnits`, `BankSeq`.
- [x] `Queue`: Enqueue with the three dedupe policies on (tenant, bank, kind, serial key) via `joinable`
      (bank row `FOR KEY SHARE` first, advisory xact lock in its own statement before the lookup, joined row
      `FOR UPDATE`; refresh_mental_model always dedupes as Pending), Upgrade (shallow merge, force_full
      OR-ed), Reschedule, CancelBank, Get, List (keyset paging), Cancel, Retry, Prune.
- [x] `Claimer`: the spec's claim SQL verbatim, then the processing update; a unique violation retries the
      claim in a fresh transaction (bounded, never surfaces as an error). Heartbeat (`*LostJobsError`),
      Progress, Alive, Complete, Fail, ReclaimStale (bank row before op row; each terminal row in its own
      tx; OPERATION_FAILED under a fresh seq, skipped for tombstoned or gone banks; the sweep never fails on
      one bank).
- [x] `pgtest` harness: one `paradedb/paradedb:0.26.1-pg17` container per test binary, bootstrap hidden by
      tmpfs (as compose), migrated with the repo's Alembic, tables truncated per test; skips without Docker.

## Acceptance tests (all pass, `go test -race`)

| Card item | Test |
|---|---|
| Every porttest suite passes on testcontainers Postgres (same image as compose) | `TestStoreContract`, `TestSeqContract`, `TestQueueContract` (`contract_test.go`) |
| Serial keys: 8 goroutines, 500 jobs, 20 keys, never two PROCESSING rows per key | `TestAcceptSerialKeys` (`queue_accept_test.go`) |
| Seq order: later commit has the higher seq; a reader seeing S sees every row <= S | `TestAcceptSeqOrder` (`seq_accept_test.go`; 8 writers x 25 txs + concurrent prefix reader) |
| Small banks: 0.1% of 50k rows still gets 10 semantic hits | `TestAcceptSmallBankVector` (`search_accept_test.go`; default plan and plan forced through HNSW; 0 hits with iterative scan off as a control; ~70 s) |
| Reclaim: stale heartbeat reclaimed; fails after max attempts | `TestAcceptReclaim` (`queue_accept_test.go`) |
| Carried forward: tenant isolation on round-1/2 write ports (store side) | `porttest.RunStoreGapsSuite` via `TestStoreGapsContract` (SetDocumentTags, Dirty/ClearDirty, PendingMaintenance/PruneOrphans, Directives) |
| Carried forward: queue gaps | `porttest.RunQueueGapsSuite` via `TestQueueGapsContract`: `CancelRetryScopedToBank`, `FailRetryOnCancelRequested` (Fail(cause, retryAt) on a cancel-requested row ends CANCELLED), `FailTombstonedBankNoEvent`, `ReclaimTombstonedBankAtMax`, `JoinUpgradeVersusClaim` (Enqueue-join + Upgrade with a concurrent claimer) |
| Carried forward: adapter obligations (READ COMMITTED, advisory lock before lookup, joined row FOR UPDATE, bank FOR KEY SHARE) | implemented in `store.go` / `queue.go`; race-relevant ones exercised by the concurrent enqueue/upgrade cases of `RunQueueSuite` and `JoinUpgradeVersusClaim` |
| Gone bank under a processing job (not buildable through ports) | `TestQueueGoneBank` |

Also: `TestRenderPredicateMatchesEval` checks the SQL rendering of `port.SQLPredicate` against `Eval`.

Gates on the rebased branch: `make gen` (no diff in gen/, schema/, internal/mocks/, db/), `make lint
BREAKING_AGAINST=contracts-v1`, `make test` pass (with `PATH=/opt/homebrew/bin:$PATH`). The in-memory fake
(WP-02, already merged) also passes both new gap suites (checked locally; not wired, see gaps).

## Decision notes

- 100 store foundation (InTx never retries `fn`, 40P01/40001 -> ErrConflict, Seq stamps
  last_memory_write_at, SetConfig upserts, id parsing, error mapping)
- 101 pgtest harness
- 110 enqueue dedupe details, 111 operation ids / List paging, 112 claim retry, 113 terminal failures and
  reclaim
- 120 semantic arm settings, 121 BM25 query form, 122 temporal arm, 123 graph arm
- 130 archive and restore, 131 entity mention_count
- 140 chunk replace, 141 bank delete batches, 142 small repo reading choices

CCRs: none. No porttest assertion contradicted the spec.

## Known gaps

- `internal/fake` does not yet run `RunStoreGapsSuite` / `RunQueueGapsSuite` (it passes them; wiring is a
  two-line test in WP-02's package, outside this card's paths).
- `internal/porttest/porttest_test.go` still has the "pending WP-01" skip placeholders; the suites now run from
  `internal/adapter/postgres/contract_test.go`. Left untouched to avoid conflicts; the orchestrator may delete
  them.
- `parked_links` is unused in v1 (archive snapshots are JSON); DeleteBatch still clears it.
- Enqueues of several dedupe keys in one tx are not globally sorted (separate calls); a deadlock surfaces as
  `ErrConflict` for the caller to retry (decision 110).
- One `make test` run failed every postgres test at once (container start, most likely contention with other
  testcontainers); the rerun passed and `pgtest.Main` now retries a failed start once.
- `TestAcceptSmallBankVector` takes ~70 s (it builds a 50k x 768 HNSW index); `-short` skips it.
