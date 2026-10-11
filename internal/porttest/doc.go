// Package porttest holds the shared contract suites every implementation of the persistence, queue and blob ports must
// pass: the Postgres adapter (internal/adapter/postgres, on testcontainers), the S3 adapter (internal/adapter/s3, on
// MinIO) and the in-memory fakes (internal/fake). The assertions come from the port doc comments, SPEC.md ("Ports",
// "Jobs", "Behavioural rules") and the readings the suites pin where those leave a choice open, so an adapter and a
// fake that both pass behave the same way to engines.
//
// # Architecture
//
// porttest sits beside the ports (domain -> port -> engine/adapter -> server/worker -> cmd/memd/wire.go): it imports
// internal/domain and internal/port only and knows no implementation. It is a regular (non-test) package so
// implementations can import it from their own _test.go files; production code never imports it. A behaviour change to
// a port starts here, then both implementations follow. The model ports (LLM, Embedder, Reranker) and the Clock,
// ModelRouter and ConfigResolver have no suite here; their fakes are tested in internal/fake.
//
// # Running a suite
//
// An implementation runs a suite from its own _test.go file by passing a factory ([StoreFactory], [QueueFactory] or
// [BlobFactory]):
//
//	func TestStoreContract(t *testing.T) {
//		porttest.RunStoreSuite(t, func(t *testing.T) port.Store { return newTestStore(t) })
//	}
//
// The Postgres adapter runs every suite below except the blob suite (contract_test.go, store_gaps_test.go,
// queue_gaps_test.go), the fake runs all of them (store_test.go, blob_test.go), and the S3 adapter runs [RunBlobSuite].
//
// # Suites
//
//   - [RunStoreSuite]: Store, Tx and Reader (commit, rollback, panic rollback, read isolation, all four recall arms
//     with their tag and seq filters, GetUnits, tenant isolation) and every repository a Tx exposes except the Queue:
//     units, documents, chunks, entities (including EntitiesAttachPrunedIsConflict: attaching an entity pruned
//     meanwhile is domain.ErrConflict), links, evidence, events, banks (including BanksTenantConfigLockSerializes:
//     LockTenantConfig serializes config read-modify-writes), mental models, and the curation and maintenance writes
//     (edits, retags, directives, graph and entity maintenance queues, batched bank deletion, event pruning).
//   - [RunStoreGapsSuite]: tenant isolation of SetDocumentTags, the entity and link maintenance queues and
//     DirectiveRepo.
//   - [RunSeqSuite]: Tx.Seq and Reader.BankSeq, the per-bank change sequence whose row lock makes seq order commit
//     order (including two concurrent writers).
//   - [RunQueueSuite]: Queue (through Tx.Queue) and Claimer: claim order, run_at, serial keys, dedupe, Upgrade,
//     Reschedule, operation control (Get, List, Cancel, Retry, Prune, CancelBank), deleting banks, tenant isolation,
//     heartbeats, reclaim, fencing of stale or cancelled workers, retries, and concurrent claims and enqueues.
//   - [RunQueueGapsSuite]: tenant isolation of Cancel and Retry, Fail with a retry on a cancel-requested job, terminal
//     failures in tombstoned banks, and the join-then-Upgrade race with a concurrent claimer.
//   - [RunReadsSuite]: the id-keyed Reader lookups and small reads (GetChunks, EntityStates, StaleScopes, TagCounts,
//     CountObservations, NewestSeqInScope, ModelsCiting), the temporal relevance filter and Document.ObservationScopes,
//     each with tenant isolation.
//   - [RunCurationReadsSuite]: GetUnit, ListUnits, ObservationHistory, BankStats, ListDocuments and ListMentalModels.
//   - [RunMaintenanceSuite]: the cross-bank maintenance reads CronModels, FailedRefreshes and BanksToConsolidate, and
//     UnitRepo.Unconsolidated's embedding filter.
//   - [RunObservationAdminSuite]: ListScopes (including the "0" cursor that continues after the shared scope),
//     UnitRepo.IDsByDocument and the evidence repository's CitingScopes, DeleteScopes and DeleteAllObservations.
//   - [RunBlobSuite]: Blob round trips, overwrite, missing keys, Delete, DeletePrefix over more keys than one S3 page,
//     and List paging.
//
// # Factory contract
//
//   - Each Run* function calls its factory once per subtest (t.Run). The factory returns an implementation over an
//     EMPTY database or bucket: no banks, tenants, units, jobs or objects. The Postgres harness shares one container
//     and truncates every table per call; the fakes build a new instance. Register cleanup with t.Cleanup on the
//     subtest's t.
//   - Subtests never call t.Parallel. Claim and ReclaimStale are global across banks, so two subtests must never share
//     a database at the same time; fixed bank names are safe for the same reason.
//   - QueueFactory returns a Store and a Claimer over the same database: jobs enqueued through Store.InTx(tx.Queue())
//     become claimable when the transaction commits.
//   - Stores must serve at least two concurrent transactions (the Seq, concurrent-claim, concurrent-enqueue and
//     concurrent-upgrade tests), and Read() must not block on an open InTx: the read-isolation test reads GetUnits and
//     BankSeq while a transaction holding the bank's Seq is open.
//   - InTx returns the callback's error so that errors.Is still matches it (wrapping is fine); domain errors from
//     repositories (ErrNotFound, ErrAlreadyExists, ErrBankDeleting, ErrFailedPrecondition) must survive the same way. A
//     panicking callback rolls back; InTx may re-panic or return an error.
//
// # Timing and fixtures
//
// Due-ness (run_at) is decided by the database clock, which may drift from the test process's clock, so run_at values
// are an hour in the past or future. Heartbeat staleness compares database time with database time, so the reclaim
// tests sleep 400ms and use a 200ms ttl. Fixed timestamps are whole seconds in UTC, so they round-trip through
// Postgres's microsecond precision exactly. Embeddings are built at known angles, so the expected cosine scores are
// exact up to a tolerance of 1e-3. Every wait on a goroutine is bounded by 15s, so a deadlocked implementation fails
// instead of hanging.
//
// # Writing tests here
//
// Never call t.Fatal inside an InTx callback or a goroutine; return an error from the callback and assert after InTx
// returns. Give every expected error (ErrAlreadyExists, ErrNotFound, ...) its own transaction, because Postgres aborts
// a transaction after a constraint violation. Enqueue jobs whose relative age matters in separate transactions: the
// suites do not assume an implementation orders one transaction's jobs by insertion. porttest_test.go checks the
// fixture helpers themselves (UUIDs, angle vectors, and that the tag predicates used against every arm select distinct
// subsets).
package porttest
