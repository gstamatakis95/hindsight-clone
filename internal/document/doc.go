// Package document holds the document-level operations beyond retain. [Service] serves DocumentService:
// listing and reading documents, replacing a document's tags ([Service.RetagDocument]) and reprocessing a
// document from its stored text ([Service.ReprocessDocument]); it also runs the retag_document job
// ([Service.RunRetagJob]).
//
// # Where it sits
//
// document is a feature package. It imports domain, the boundaries (store, blob, clock) and jobs, and never
// another feature, an adapter, gen, pgx, net/http, the S3 SDK or Datadog (depguard). Reprocessing builds the
// retain_document payload itself (jobs.RetainPayload) and enqueues it through the jobs catalogue, so the
// memory feature runs it without either package importing the other. cmd/memd/wire_document.go builds the
// Service, hands it to grpcapi's DocumentHandler and registers RunRetagJob for the retag_document kind.
//
// # Retag: RetagDocument
//
// [Service.RetagDocument](ctx, bank, doc, tags) runs in one DB.InTx:
//
//  1. Read the bank (unknown = domain.ErrNotFound, tombstoned = domain.ErrBankDeleting) and the document
//     (unknown = domain.ErrNotFound).
//  2. If the normalized new set equals the stored one (any order), it is a no-op: nothing is written, no
//     change seq is taken, and the zero Operation is returned. The exception: if the document's newest
//     retag_document operation (serial key "doc:{id}", found by paging Queue.List newest first) ended FAILED
//     or CANCELLED, it takes tx.Seq and enqueues retag_document again, since that job's observation clearing
//     never ran.
//  3. On a real change it takes tx.Seq(bank) (the bank-row lock that serializes concurrent retags and
//     retain batches), re-reads the document under the lock (a concurrent retag that already set the same
//     tags makes this a no-op), then stores the new tags on the document, rewrites its units' tags with
//     UnitRepo.SetDocumentTags(old, new, seq) and enqueues retag_document (serial key "doc:{id}",
//     DedupePending, payload jobs.RetagPayload; jobs.EnqueueRetag).
//
// It then reads the enqueued operation in a second short transaction (a bank tombstoned meanwhile reads as
// domain.ErrNotFound). SetDocumentTags computes (tags - old) + new per unit, so label tags written by
// entity_labels with write_to_tags survive; a tag that is both a document tag and a label tag cannot be told
// apart and leaves with the document tag. Because the document row and its units change in the same
// transaction, a unit's document-derived tags always equal its document's tags. The units' tags change when
// RetagDocument returns; their old observations remain until the job runs.
//
// # Retag job: retag_document
//
// [Service.RunRetagJob](ctx, run) first reports progress (stage retag_document, 0 of 1), which is its cancel
// check. It is idempotent and re-reads everything; the payload carries only the document id (the tags live on
// the document row, so a retag that joined a pending one runs with the latest tags). In one transaction it:
//
//  1. takes tx.Seq(bank); a gone bank (domain.ErrNotFound) or a tombstoned one (domain.ErrBankDeleting)
//     fails here, and it never creates a bank;
//  2. reads the document; a document that is gone ends the job as a successful no-op;
//  3. calls SetDocumentTags(cur, cur, seq), which leaves the tags as they are, stamps the live units with
//     seq, clears their consolidation stamps and returns their ids;
//  4. deletes the observations citing those units and resets their co-sources (EvidenceRepo.DeleteCiting);
//     if any were deleted it enqueues graph_maintenance (serial key "graph", DedupePendingOrRunning);
//  5. enqueues consolidate (serial key "consolidate", payload {}, DedupePending), which re-consolidates the
//     units under their new tags.
//
// A malformed payload is domain.ErrInvalidArgument. An error after ctx ended, or a gone bank, is returned
// as domain.ErrCancelled. Running the job twice repeats only the stamp reset and finds nothing more to
// delete. A retain_document job that is running while a retag lands adopts the new tags at its next batch
// (the batch reads them under the same Seq lock) instead of being superseded.
//
// # Reprocess: ReprocessDocument
//
// [Service.ReprocessDocument] makes no model call. In one transaction it takes tx.Seq(bank) first (which locks
// the bank and refuses a tombstoned one), reads the document (unknown = domain.ErrNotFound), replaces its
// stored overrides when new ones are given, rebuilds the retain_document payload from the stored document
// (its blob key and content hash, tags and item-level observation scopes; a document without stored text is
// domain.ErrFailedPrecondition) and from one of its units (context, metadata, mention time and timeless flag:
// the newest unit never curated, else the newest), re-upserts the document and enqueues the forced
// retain_document job (ForceReextract) with the document's overrides. It returns that operation, read in a
// second short transaction. Entity hints are not stored and are lost on reprocess.
//
// # Listing and reading
//
//   - ListDocuments returns one keyset page, newest first (created_at, then id). page_size follows
//     [domain.ListPage] (0 means 100, larger values are capped at 1000, a negative size is
//     domain.ErrInvalidArgument). The tag filter is flat only, its match defaults to any_strict and its tags are
//     normalized. The page token is
//     opaque base64url JSON of (created_at, id), and a malformed token is domain.ErrInvalidArgument. An
//     unknown or tombstoned bank is domain.ErrNotFound.
//   - GetDocument reads the document in a short transaction (tombstoned bank = domain.ErrNotFound) and,
//     only when asked, its original text from the blob store. A missing blob returns the document with
//     empty text; a read failure is domain.ErrUnavailable.
//
// No method here calls a model, so every transaction is short; the Postgres DB.InTx re-runs a transaction up
// to 3 times in all after a deadlock (40P01) or serialization failure (40001), and every transaction body
// here re-reads its state, so a re-run is safe.
//
// # Tests
//
// The tests run on a pgtest database: the set-equal no-op, observation clearing on a real change, the job
// reading current tags, unknown documents, re-enqueue after a failed retag, label
// tags kept, cancellation mid-transaction, listing and paging, reading with and without text, a missing blob,
// and reprocessing (payload, scopes, overrides, a curated unit ignored, unknown document, deleting bank).
// SPEC "Behavioural rules" (UpdateDocument), "Jobs" (retag_document) and "gRPC surface" (DocumentService).
package document
