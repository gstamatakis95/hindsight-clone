// Package bank owns the bank lifecycle. Its one type, [Service], built by [New] from [Deps], serves the bank part
// of memory.v1.BankService: creating a bank (plain or from a template), reading it, its stats and tag listings,
// exporting it as a template and the two-step delete. It runs the delete_bank job ([Service.RunDeleteJob]) and
// the maintenance tick's resume_bank_deletes sweep ([Service.ResumeStalledDeletes]).
//
// # Where it sits
//
// bank is a service. It imports domain, the boundaries (store, blob, clock) and jobs, and never another service, an
// adapter, grpcapi, maintenance, gen, pgx, net/http, the S3 SDK or Datadog (depguard). A template's mental models
// get their first build through the jobs catalogue (jobs.QueueModelRefresh), so the mentalmodel service runs it
// without either package importing the other. cmd/memd/wire_bank.go builds the Service, hands it to grpcapi's
// BankHandler and maintenance, and registers RunDeleteJob for the delete_bank kind. The bank's config, directives
// and prompt preview belong to internal/configsvc.
//
// The public surface:
//
//   - RPCs: [Service.CreateBank] (CreateBank and CreateBankFromTemplate), [Service.GetBank], [Service.DeleteBank],
//     [Service.GetBankStats], [Service.ListTags] and [Service.ExportBankTemplate].
//   - Job: [Service.RunDeleteJob] handles delete_bank.
//   - Sweep: [Service.ResumeStalledDeletes].
//
// # Bank existence rules
//
// Reads of an unknown or tombstoned bank are domain.ErrNotFound. Writes call BankRepo.Ensure, which creates an
// unknown bank with tenant defaults, or reports domain.ErrBankDeleting (FAILED_PRECONDITION) for a tombstoned one.
// SPEC "Behavioural rules" (Banks and documents).
//
// # Create
//
// [Service.CreateBank] without a template inserts the bank with its own config: an existing bank is
// domain.ErrAlreadyExists (with its own message, never the store's, also when a concurrent create wins the race)
// and a tombstoned one domain.ErrBankDeleting. It takes no change seq.
//
// With a template it creates the bank in one transaction: Banks().Create, tx.Seq(bank), the directives with new
// ids, each mental model, and each model's first build enqueued with jobs.QueueModelRefresh (which expects tx.Seq
// to have been taken); it returns the build operations in template order. An existing bank fails in Create, a
// template model without an id is domain.ErrInvalidArgument, and any failure rolls the whole bank back.
//
// [Service.GetBank] returns a tombstoned bank too, with DeletingAt set; a client watches for NOT_FOUND to learn
// that a delete finished. [Service.ExportBankTemplate] returns a bank's own config, its directives (ids, bank refs
// and timestamps dropped) and its mental-model specs, with no memories.
//
// # Stats and tags
//
// [Service.GetBankStats] returns the bank's counts from indexed COUNT queries (no counter tables).
// [Service.ListTags] pages tags with counts from a [ListTagsRequest]: source defaults to memories (live units), the
// prefix is normalized like a tag, page_size 0 means [DefaultPageSize] (100) and larger sizes are capped at
// [MaxPageSize] (1000), and the opaque page token carries the last tag of the previous page (a malformed token is
// domain.ErrInvalidArgument). It asks the store for one row more than the page to learn whether a next page
// exists.
//
// # Bank delete
//
// A bank is deleted in two steps (SPEC "Behavioural rules", DeleteBank):
//
//  1. [Service.DeleteBank] tombstones it in one transaction: read the bank (unknown = domain.ErrNotFound), set
//     deleting_at (MarkDeleting, which locks the bank row), cancel the bank's pending jobs (Queue.CancelBank
//     spares delete_bank) and enqueue delete_bank (jobs.EnqueueDeleteBank: serial key "bank", payload {},
//     DedupePendingOrRunning), returning that operation. No tx.Seq is taken: Seq refuses a tombstoned bank.
//     From then on writes fail with domain.ErrBankDeleting and reads with domain.ErrNotFound. Re-issuing
//     DeleteBank returns the running delete, or a new one when the previous delete_bank failed or was
//     cancelled, which resumes a half-deleted bank.
//  2. [Service.RunDeleteJob] runs up to MaxBatches committed transactions, each removing up to BatchSize rows
//     with BankRepo.DeleteBatch. When a batch finds nothing left, it finishes: check jobs.Run.Alive (the job
//     still holds its operation, else domain.ErrCancelled) and that the bank is still tombstoned; if another job
//     of the bank is still PROCESSING (a handler winding down after the tombstone) it enqueues its own
//     continuation [DeleteBlockedDelay] (5 s) later and stops; otherwise it deletes the blobs under
//     {tenant}/{bank}/, then in a last transaction re-checks for processing jobs and calls BankRepo.Delete,
//     which removes the bank row with its remaining events and operations.
//
// While work is left (the batch budget ran out, or a processing job blocked the finish) the job re-enqueues
// itself (jobs.Requeue, DedupePending). delete_bank retries without an attempt cap, like consolidate: a transient
// failure on the kind's last attempt enqueues a continuation after jobs.Backoff before the error fails the row,
// so a half-deleted bank is never abandoned. The maintenance tick's [Service.ResumeStalledDeletes] also
// re-enqueues delete_bank for a tombstoned bank that has none pending or running (ReclaimStale and the shutdown
// release charge attempts too), so a delete survives crashes.
//
// A run that finds the bank gone, or no longer tombstoned (a fresh bank took the id), is done and deletes no
// blobs, so a stale duplicate run never touches a new bank's data. No job outlives its bank. The returned
// operation can complete, or vanish with the bank row, while a continuation still runs; GetBank returning
// NOT_FOUND is the completion signal. Defaults ([DeleteConfig] zero values): [DefaultDeleteBatchSize] 1000 rows,
// [DefaultDeleteMaxBatches] 100 batches per run, [DefaultDeleteBlobPageSize] 1000 keys.
//
// # Blob delete
//
// The blobs go page by page with the fence checked before every page after the first: List one page of keys under the
// bank prefix, then DeletePrefix each listed key (every listed key lies under the bank prefix being emptied, so nothing
// outside it is touched). blob.Store.List returns a short page only when nothing is left, so a page shorter than
// BlobPageSize is the last one and the delete ends without another List. blob.Store keeps exactly four operations, and
// its DeletePrefix takes no limit: one DeletePrefix of the whole bank prefix would put the entire delete inside one
// fence window, so the deleter stays per key. On S3 a per-key DeletePrefix costs a list and a delete request; a bounded
// delete would need a fifth blob.Store operation.
//
// # Transactions
//
// Every transaction here is short and calls no model; the Postgres DB.InTx re-runs a transaction up to 3 times
// in all after a deadlock (40P01) or serialization failure (40001), and every body resets its outputs and
// re-reads its state, so a re-run is safe.
//
// # Tests
//
// Tests run on a pgtest database and fake.Blob: bank create and its error cases, stats and tag paging, template
// round trip and rollback, and the delete lifecycle (idempotent tombstone, resumption of a half-deleted bank,
// paged blob deletes with fencing, the processing-job re-check in the final transaction, a cancelled run, the
// continuation and attempt-cap requeue).
package bank
