// Package store declares the Postgres boundary: transactions ([DB], [Tx]), the read side ([Reader], split by consumer
// into [Search], [Catalog] and [Freshness]), the repositories a transaction exposes, the job queue ([Queue], [Claimer])
// and the cross-bank scans of the maintenance tick ([Maintenance]).
//
// # Where it sits
//
// store imports only internal/domain and the standard library. internal/store/postgres is its one implementation:
// *postgres.DB implements DB, Claimer and Maintenance. Everything else depends on these interfaces and never on pgx;
// cmd/memd is the only place the concrete type appears. Errors that cross the boundary are already domain error
// classes, so callers test them with errors.Is (wrapcheck ignores this package).
//
// Besides interfaces, the package holds only the types the interfaces reference: the query, hit and cursor types
// ([VectorQuery], [TextQuery], [TemporalQuery], [GraphQuery], [Hit], [UnitQuery], [DocumentQuery], [ModelQuery],
// [ScopeQuery], ...), the job types ([Job], [DedupePolicy], [OperationFilter], [LostJobsError]) and the tag filter tree
// [SQLPredicate] with its reference semantics [SQLPredicate.Eval].
//
// There is no mock or fake of this package: the Postgres implementation is tested on real Postgres
// (internal/store/postgres/pgtest), and so is every caller. The doc comment on each interface method is its
// specification; a change starts in that comment and the adapter's tests.
//
// # Transactions and the change sequence
//
// [DB.InTx] runs a function in one READ COMMITTED transaction; a state change and the jobs it enqueues commit
// together. No LLM, embedding or rerank call ever runs inside InTx: read, call the model, then write in a short
// transaction. [DB.Read] returns the long-lived [Reader] the stateless API path uses; each call is its own statement
// and never blocks on an open transaction.
//
// [Tx.Seq] locks the bank row and returns the transaction's change sequence. Every transaction that changes memory
// content calls it first, before any read it depends on and before any Enqueue, and stamps the rows and lifecycle
// events it writes with the value, so within a bank seq order is commit order; [SeqRange] lets readers (mental-model
// refreshes) read a stable snapshot or only what changed. [BankRepo.LockTenantConfig] serializes read-modify-writes of
// a tenant's config. The Postgres adapter re-runs a transaction that loses a deadlock or serialization race, or whose
// [EntityRepo.Attach] hit an entity pruned meanwhile, so the function passed to InTx must reset its captured outputs at
// the top and keep no side effects outside the database.
//
// A minimal write:
//
//	err := db.InTx(ctx, func(tx store.Tx) error {
//		seq, err := tx.Seq(ctx, bank)
//		if err != nil {
//			return err
//		}
//		u.Seq = seq
//		if err := tx.Units().Insert(ctx, []domain.Unit{u}); err != nil {
//			return err
//		}
//		_, _, err = tx.Queue().Enqueue(ctx, store.Job{Bank: bank, Kind: kind, SerialKey: key,
//			Dedupe: store.DedupePending})
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
package store
