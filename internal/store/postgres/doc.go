// Package postgres is memd's production persistence adapter. One [DB] implements store.DB (with its store.Tx,
// store.Reader and every repository), store.Maintenance and jobs.Claimer over a pgx pool and the sqlc-generated
// queries in internal/store/postgres/db. Postgres with pgvector (HNSW cosine), pg_search (BM25) and pg_trgm serves the
// four recall arms, the bank change sequence (Tx.Seq) and the job queue (FOR UPDATE SKIP LOCKED claims, heartbeats,
// reclaim).
//
// # Architecture
//
// The package is the adapter of internal/store. It imports internal/store, internal/domain, its own db subpackage, pgx
// and pgvector, and never internal/engine, internal/grpcapi or internal/jobs. It holds no business logic: scoring,
// budgets, config merging and deciding what to enqueue belong to the engines (internal/store/AGENTS.md). Alembic owns
// the schema (migrations/, dumped to schema/schema.sql); this package never runs DDL.
//
// [New] returns the concrete *DB; cmd/memd, the only composition root, narrows it to store.DB, store.Maintenance and
// jobs.Claimer.
//
//	store := postgres.New(pool) // over a caller-owned pool
//	err := store.InTx(ctx, func(tx store.Tx) error {
//		seq, err := tx.Seq(ctx, bank) // locks the bank row until commit
//		...                           // writes stamped with seq
//	})
//	hits, err := store.Read().SearchVector(ctx, q) // each Read() call is its own statement
//
// [New] wraps a pool the caller owns and closes. The pool needs at least two connections: DB.Read never waits on
// an open transaction. In production cmd/memd sizes the pool (MEMD_DB_MAX_CONNS, default
// max(2*MEMD_WORKER_SLOTS+16, 32)) and, when Datadog is enabled, installs the pgx query tracer from
// internal/observability on the pool config; this package itself reads no environment variables and imports nothing
// from Datadog.
//
// # Transactions
//
// [DB.InTx] runs fn in one READ COMMITTED transaction, commits when fn returns nil, and rolls back on an error or a
// panic (the panic is re-raised). fn's error is returned as is. A transaction that fails with a deadlock (SQLSTATE
// 40P01), a serialization failure (40001) or the pruned-entity conflict (see Entities below) is rolled back and fn runs
// again in a fresh transaction, up to 3 attempts in all, stopping early when ctx is done. This is safe because fn never
// calls an LLM, embedder or reranker (store.DB): a re-run repeats only short database work, so fn must keep no side
// effects outside the database. After the last attempt the caller sees domain.ErrConflict.
//
// Tx.Seq bumps the bank's change_seq with one UPDATE that also stamps last_memory_write_at (every memory change passes
// through it, so that is the one place to stamp it); the value is cached per bank for the rest of the transaction, and
// the bank row stays locked until commit, so within a bank seq order is commit order (TestAcceptSeqOrder). A tombstoned
// bank is domain.ErrBankDeleting and an unknown one domain.ErrNotFound. Every transaction that changes memory content
// calls Seq first and stamps its rows with the value, and callers that read-modify-write a bank row (UpdateBankConfig)
// call Seq before re-reading it, because under READ COMMITTED a read taken before the lock can be stale.
// BankRepo.LockTenantConfig takes the transaction-scoped advisory lock
// pg_advisory_xact_lock(hashtextextended('tenant_config/' || tenant, 0)), which serializes read-modify-writes of one
// tenant's config even before the tenant has a row; plain TenantConfig reads, which sit on hot paths, never wait on it.
//
// The Reader returned by [DB.Read] runs each call as its own statement on the pool; calls that need
// transaction-local settings (the vector search's SET LOCAL) open a short read-only transaction. A Tx embeds the same
// reader bound to its own transaction, so reads inside InTx see the transaction's writes.
//
// # Recall arms
//
// The searches build their SQL at run time, because the tag filter is a variable tree (domain.TagPredicate) that sqlc
// can't express; every value is a bind parameter with an explicit cast and only fixed SQL text is concatenated. Each
// arm filters by bank, fact types, tag predicate and seq range, reads only memory_units (live units; archived ones live
// in memory_units_archive) and breaks score ties by id.
//
//   - SearchVector: HNSW cosine search; the transaction first sets hnsw.ef_search = 200, hnsw.iterative_scan =
//     strict_order and hnsw.max_scan_tuples = 100000, so a small bank on the one shared index still gets its hits.
//     Score is cosine similarity; a query embedding of the wrong dimension is domain.ErrInvalidArgument.
//   - SearchText: pg_search BM25 over text and context through the ||| operator, so user punctuation is never parsed as
//     query syntax. Score is paradedb.score(id).
//   - SearchTemporal: units whose span overlaps the half-open window, scored by proximity to the window's centre,
//     optionally spread round-robin over Buckets sub-windows and filtered by embedding similarity.
//   - Graph().Expand (graph.Reader, also Tx.Graph): a recursive CTE over the bank's forward links in memory_links,
//     at most 2 hops, scored by the best product of link weights; seeds are never hits; Types, Tags and Seq apply
//     in SQL before the limit.
//
// # Job queue
//
// Tx.Queue is store.Queue inside the caller's transaction. Enqueue takes FOR KEY SHARE on the bank row (refusing a
// tombstoned bank with domain.ErrBankDeleting except for its delete_bank job), then, for a job that dedupes, the
// advisory lock pg_advisory_xact_lock(hashtextextended(tenant/bank/kind/serial_key, 0)) in its own statement, then
// looks up the oldest joinable row FOR UPDATE, and inserts only when there is none, so two concurrent enqueuers of one
// key never both insert. The effective dedupe policy: no serial key never dedupes, refresh_mental_model always dedupes
// as Pending, a job with overrides never dedupes. New ids are UUIDv7 and created_at is clock_timestamp(), so jobs of
// one transaction keep their order. List pages newest first with a keyset token (created_at, id), page size default 50,
// cap 500.
//
// The Claimer methods run on the pool, outside any caller transaction:
//
//   - [DB.Claim] runs the SPEC "Jobs" claim SQL verbatim (only $1 gains a ::text[] cast): due pending jobs of the
//     given kinds whose serial key is free, oldest first, FOR UPDATE OF o SKIP LOCKED, then marks them PROCESSING under
//     the worker. A lost race on the unique processing index or a deadlock between claimers retries in a new
//     transaction, up to 5 tries, and then returns an empty batch, never an error. A row whose stored overrides can't
//     be decoded is failed in place, with no event.
//   - [DB.Heartbeat] refreshes the worker's PROCESSING rows and returns a *store.LostJobsError naming each id the
//     worker no longer holds or whose cancel was requested.
//   - [DB.Progress], [DB.Complete] and [DB.Fail] are fenced on status = 'processing'; a fenced update that
//     matches nothing is domain.ErrNotFound (unknown id) or domain.ErrFailedPrecondition. A cancel-requested job ends
//     CANCELLED instead and the call returns domain.ErrCancelled.
//   - A terminal Fail, and [DB.ReclaimStale] at a kind's max attempts, end the job FAILED in a short transaction
//     that bumps the bank's seq first (bank row before operation row) and appends one OPERATION_FAILED event at that
//     seq; a tombstoned or gone bank takes no seq and gets no event.
//   - ReclaimStale sweeps PROCESSING rows whose heartbeat is older than the ttl (database time): below the kind's max
//     attempts they return to PENDING with attempts+1, cancel-requested ones become CANCELLED, and the rest fail one by
//     one, each in its own transaction. One row's error never stops the sweep; errors are joined and returned with the
//     count.
//
// # Entities, archive and bank deletion
//
// The store maintains entities.mention_count: Attach and Restore add a mention per new link, Detach, DeleteByDocument
// and Archive take one off. Attach locks the entities in id order before linking, so a concurrent PruneOrphans (graph
// maintenance, which locks before checking references) either sees the new rows or deletes the entities first. If fewer
// entities lock than were requested, Attach attaches nothing and returns an error wrapping domain.ErrConflict, and InTx
// re-runs the whole transaction, whose fresh entity resolution no longer finds the pruned entity; silently skipping it
// would lose the unit's entity.
//
// UnitRepo.Archive moves a unit to memory_units_archive with JSON snapshots of its entities and causal links; Restore
// brings it back without an embedding (rederive recomputes it). ChunkRepo.Insert replaces a chunk at a committed index
// by deleting the old row, whose units keep living with chunk_id NULL. Tags written to units, documents and directives
// must already be in domain.NormalizeTags form, else domain.ErrInvalidArgument.
//
// DocumentRepo.Upsert is the only write that clears documents.extracted_at and DocumentRepo.Complete the only one that
// sets it (with the content and overrides hashes of the finished retain). Retain treats a document whose extracted_at
// is set and whose hashes match as unchanged, and a run that starts replacing a completed document's units re-upserts
// it first, so a document whose units are being replaced is never stamped complete.
//
// BankRepo.DeleteBatch deletes up to limit rows of a tombstoned bank, table by table, children first, so cascades only
// reach tables already emptied and the count is exact; 0 means only the bank row, its events and its operations are
// left. BankRepo.Delete then deletes the bank row first (an Enqueue holding FOR KEY SHARE makes it wait, and a later
// one finds no bank), the tables without a bank foreign key, the lifecycle events and the operations, the running
// delete_bank row included. Both refuse a live bank with domain.ErrFailedPrecondition.
//
// # Maintenance scans
//
// DB implements store.Maintenance, the cross-bank scans of the maintenance tick: [DB.StrandedFacts] (facts left
// without an embedding by a failed rederive), [DB.SettleRetainBatches] (ends retain_batch parents whose children are
// all terminal), [DB.UnconsolidatedBanks], [DB.StalledBankDeletes], [DB.BanksWithGraphWork], [DB.CronModels] and
// [DB.FailedRefreshes]. [DB.QueueStats] is the one read outside the tick: the job queue's depth by kind and status
// and the age of each kind's oldest due pending job, which the optional Datadog metrics sample every few seconds (an
// index scan of the partial index over pending and processing rows, so the terminal rows kept for retention cost
// nothing).
//
// # Errors
//
// Every driver error is mapped to a domain error class, wrapped with %w so both errors.Is(err, domain.ErrX) and the
// driver detail survive:
//
//   - no rows, foreign-key violation (23503): domain.ErrNotFound;
//   - unique violation (23505): domain.ErrAlreadyExists;
//   - check (23514) and not-null (23502) violations, program limit exceeded (54000) and every data exception (class
//     22): domain.ErrInvalidArgument, so a job fails at once instead of retrying;
//   - serialization failure (40001), deadlock (40P01), lock not available (55P03): domain.ErrConflict;
//   - anything else, connection loss and timeouts included: domain.ErrUnavailable.
//
// Errors that already carry a domain class pass through unchanged, and context errors keep their identity. A malformed
// server-generated id in a lookup matches nothing (domain.ErrNotFound, or skipped in list reads); in an insert it is
// domain.ErrInvalidArgument, and an empty one gets a fresh UUID.
//
// # Testing
//
// The tests run on real Postgres from internal/store/postgres/pgtest: every test and case gets its own database
// cloned from a migrated template, so they run in parallel; without Docker they skip unless MEMD_REQUIRE_DOCKER is
// set. TestStore (DB, Tx, Reader, every repository, the curation writes and tenant isolation), TestSeq, TestQueue
// (Queue and Claimer), TestReads, TestCurationReads, TestMaintenanceReads and TestObservationAdmin are case tables
// over the store semantics; the *_accept_test.go files, store_sql_test.go and the race tests pin behaviour that needs
// raw SQL or load: seq order, claims, reclaim, the small-bank HNSW search and
// TestAttachAfterConcurrentPruneReresolves. The config-patch locking is tested in internal/configsvc.
//
// Governing SPEC sections: "Ports", "Jobs", "Behavioural rules" and "Recall pipeline".
package postgres
