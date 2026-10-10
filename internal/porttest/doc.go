// Package porttest holds the shared contract suites every implementation of the persistence, queue and
// blob ports must pass: the Postgres adapter (internal/adapter/postgres, on testcontainers), the S3 adapter
// (internal/adapter/s3, on MinIO) and the in-memory fakes (internal/fake). The assertions come from the
// port doc comments and SPEC.md ("Ports", "Jobs", "Behavioural rules"), so an adapter and a fake that both
// pass behave the same way to engines.
//
// An implementation runs a suite from its own _test.go file by passing a factory:
//
//	func TestStoreContract(t *testing.T) {
//		porttest.RunStoreSuite(t, func(t *testing.T) port.Store { return newTestStore(t) })
//	}
//
// The suites are:
//
//   - RunStoreSuite: Store, Tx and Reader (commit, rollback, read isolation, all four recall arms, GetUnits)
//     and every repository a Tx exposes except the Queue.
//   - RunSeqSuite: Tx.Seq and Reader.BankSeq, the per-bank change sequence (including two concurrent writers).
//   - RunQueueSuite: Queue (through Tx.Queue) and Claimer.
//   - RunBlobSuite: Blob.
//
// Factory contract:
//
//   - Each Run* function calls its factory once per subtest (t.Run). The factory returns an implementation
//     over an EMPTY database or bucket: no banks, tenants, units, jobs or objects. The Postgres harness
//     shares one container and truncates every table per call; the fakes build a new instance. Register
//     cleanup with t.Cleanup on the subtest's t.
//   - Subtests never call t.Parallel. Claim and ReclaimStale are global across banks, so two subtests must
//     never share a database at the same time; fixed bank names are safe for the same reason.
//   - QueueFactory returns a Store and a Claimer over the same database: jobs enqueued through
//     Store.InTx(tx.Queue()) become claimable when the transaction commits.
//   - Stores must serve at least two concurrent transactions (the Seq and concurrent-claim tests), and Read()
//     must not block on an open InTx: the read-isolation test reads while a write transaction is open.
//   - InTx returns the callback's error so that errors.Is still matches it (wrapping is fine); domain errors
//     from repositories (ErrNotFound, ErrAlreadyExists, ErrBankDeleting, ErrFailedPrecondition) must
//     survive the same way. A panicking callback rolls back; InTx may re-panic or return an error.
//
// Timing: due-ness (run_at) is decided by the database clock, which may drift from the test process's clock,
// so run_at values are an hour in the past or future. Heartbeat staleness compares database time with
// database time, so the reclaim tests sleep 400ms and use a 200ms ttl. Fixed timestamps are whole seconds in
// UTC, so they round-trip through Postgres's microsecond precision exactly.
//
// Writing tests here: never call t.Fatal inside an InTx callback or a goroutine; return an error from the
// callback and assert after InTx returns. Give every expected error (ErrAlreadyExists, ErrNotFound, ...) its
// own transaction, because Postgres aborts a transaction after a constraint violation. Enqueue jobs whose
// relative age matters in separate transactions: now() is constant within one, so created_at would tie.
package porttest
