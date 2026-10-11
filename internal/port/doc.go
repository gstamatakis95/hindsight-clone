// Package port declares the interfaces engines use to reach the outside world: persistence ([Store], [Tx], [Reader] and
// the repositories), the Postgres job queue ([Queue], [Claimer]), blob storage ([Blob]), the AI gateway ([LLM],
// [Embedder], [Reranker]), model routing ([ModelRouter]), config resolution ([ConfigResolver]) and the clock ([Clock]).
//
// # Architecture
//
// memd is laid out as ports and adapters: domain -> port -> engine/adapter -> server/worker -> cmd/memd/wire.go. port
// imports only internal/domain and the standard library (depguard also keeps Datadog out). Engines depend on these
// interfaces and never on an adapter, pgx, net/http or the S3 SDK; adapters in internal/adapter implement them;
// cmd/memd/wire.go is the only place a concrete type is assigned to a port. Errors that cross a port are already domain
// error classes, so callers test them with errors.Is (wrapcheck ignores this package).
//
// Besides interfaces, the package holds only the types the interfaces reference: the query, hit and request types
// ([VectorQuery], [TextQuery], [TemporalQuery], [GraphQuery], [Hit], [UnitQuery], [DocumentQuery], [ModelQuery],
// [ScopeQuery], [StructuredRequest], [ChatRequest], ...), the job types ([Job], [JobKind], [DedupePolicy],
// [OperationFilter], [LostJobsError]) and the tag filter tree [SQLPredicate] with its reference semantics
// [SQLPredicate.Eval]. Identity types ([BankRef], [ScopeKey], [UnitID], [OperationID], ...), [ModelSlot],
// [ModelBinding] and [Usage] are aliases of their internal/domain definitions, so domain values carry them without
// importing port.
//
// # Implementations
//
//   - Store, Tx, Reader, every repository, Queue and Claimer: internal/adapter/postgres (production) and
//     internal/fake.Store.
//   - Blob: internal/adapter/s3 and internal/fake.Blob.
//   - LLM, Embedder, Reranker: internal/adapter/gateway and the scripted fakes in internal/fake.
//   - ModelRouter: internal/engine/routing (and a slot router in cmd/memd); ConfigResolver: internal/config; Clock: a
//     system clock in cmd/memd and internal/fake.Clock.
//
// Mockery generates a mock of every interface into internal/mocks/port (`make gen`).
//
// The persistence, queue and blob ports have shared contract suites in internal/porttest that the production adapter
// and the fake both pass, so engines tested on the fake behave the same on Postgres and S3. The doc comment on each
// interface method is its specification; a change starts in that comment and the suite.
//
// # Transactions and the change sequence
//
// [Store.InTx] runs a function in one READ COMMITTED transaction; a state change and the jobs it enqueues commit
// together. No LLM, embedding or rerank call ever runs inside InTx: read, call the model, then write in a short
// transaction. [Store.Read] returns the long-lived [Reader] the stateless API path uses; each call is its own statement
// and never blocks on an open transaction.
//
// [Tx.Seq] locks the bank row and returns the transaction's change sequence. Every transaction that changes memory
// content calls it and stamps the rows and lifecycle events it writes with the value, so within a bank seq order is
// commit order; [SeqRange] lets readers (mental-model refreshes) read a stable snapshot or only what changed.
// [BankRepo.LockTenantConfig] serializes read-modify-writes of a tenant's config. The Postgres adapter re-runs a
// transaction that loses a deadlock or serialization race, or whose [EntityRepo.Attach] hit an entity pruned meanwhile,
// so the function passed to InTx must keep no side effects outside the database.
//
// A minimal write:
//
//	err := store.InTx(ctx, func(tx port.Tx) error {
//		seq, err := tx.Seq(ctx, bank)
//		if err != nil {
//			return err
//		}
//		u.Seq = seq
//		if err := tx.Units().Insert(ctx, []domain.Unit{u}); err != nil {
//			return err
//		}
//		_, _, err = tx.Queue().Enqueue(ctx, port.Job{Bank: bank, Kind: kind, SerialKey: key,
//			Dedupe: port.DedupePending})
//		return err
//	})
//
// # Jobs
//
// The queue is the operations table; there is no broker. [Queue] is the producer side inside a transaction: Enqueue
// with a [DedupePolicy] on (bank, kind, serial key), Upgrade of a joined row, Reschedule, and operation control (Get,
// List, Cancel, Retry, Prune, CancelBank). [Claimer] is the worker side, each call its own short transaction: Claim
// (FOR UPDATE SKIP LOCKED, one PROCESSING job per serial key), Heartbeat (which returns a [LostJobsError] for jobs the
// worker must stop), Progress, Alive, Complete, Fail and ReclaimStale. Progress, Complete and Fail are fenced on a
// PROCESSING row; a cancel-requested job ends CANCELLED. Job handlers must be idempotent and re-read state, because a
// job can run twice after a reclaim.
//
// # Data-safety rules
//
// There is no per-memory delete: UnitRepo.Archive invalidates a unit into memory_units_archive and Restore brings it
// back; a document's units are replaced through DeleteByDocument and re-insertion. A write to an unknown bank creates
// it with tenant defaults ([BankRepo.Ensure]); a tombstoned bank refuses writes and enqueues with
// domain.ErrBankDeleting (except its delete_bank job). [EventRepo.List] treats limit <= 0 as no limit; API callers
// bound it themselves (the ListEvents RPC defaults to 100 and caps at 1000).
//
// # Governing documents
//
// SPEC "Ports", "Jobs", "Behavioural rules" and "Recall pipeline".
package port
