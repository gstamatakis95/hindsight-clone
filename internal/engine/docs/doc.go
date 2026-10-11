// Package docs holds the document-level operations beyond retain: UpdateDocument's tag replacement and the
// retag_document job ([Retagger]), and listing, reading and reprocessing documents ([DocumentAdmin]).
//
// # Where it sits
//
// docs is an engine package: it imports domain and port only and must never import adapters, server,
// worker, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It declares the small interfaces it
// consumes instead of importing other engines: [DocumentSource] (port.Reader satisfies it) and
// [Reprocessor] (retain.NewReprocessor satisfies it, since the retain engine owns the retain_document
// payload). cmd/memd/wire.go builds [NewRetagger](store) and [NewDocumentAdmin]; the server's
// DocumentService calls both, and the worker's retag_document handler calls [Retagger.Apply]. The request,
// page and view types ([ListDocumentsRequest], [DocumentPage], [DocumentView]) are declared here and
// aliased by the server.
//
// # Retag: UpdateDocument
//
// [Retagger.Retag](ctx, bank, doc, tags) runs in one Store.InTx:
//
//  1. Read the bank (unknown = domain.ErrNotFound, tombstoned = domain.ErrBankDeleting) and the document
//     (unknown = domain.ErrNotFound).
//  2. If the normalized new set equals the stored one (any order), it is a no-op: nothing is written, no
//     change seq is taken, and the returned id is empty. The exception: if the document's newest
//     retag_document operation (serial key "doc:{id}", found by paging Queue.List newest first) ended FAILED
//     or CANCELLED, Retag takes tx.Seq and enqueues retag_document again, since that job's observation
//     clearing never ran.
//  3. On a real change it takes tx.Seq(bank) (the bank-row lock that serializes concurrent retags and
//     retain batches), re-reads the document under the lock (a concurrent retag that already set the same
//     tags makes this a no-op), then stores the new tags on the document, rewrites its units' tags with
//     UnitRepo.SetDocumentTags(old, new, seq) and enqueues retag_document (serial key "doc:{id}",
//     DedupePending, payload [RetagPayload]).
//
// SetDocumentTags computes (tags - old) + new per unit, so label tags written by entity_labels with
// write_to_tags survive; a tag that is both a document tag and a label tag cannot be told
// apart and leaves with the document tag. Because the document row and its units change in the same
// transaction, a unit's document-derived tags always equal its document's tags. The units' tags change when
// UpdateDocument returns; their old observations remain until the job runs.
//
// # Retag job: retag_document
//
// [Retagger.Apply](ctx, job) is idempotent and re-reads everything; the payload carries only the
// document id (the tags live on the document row, so a retag that joined a pending one runs
// with the latest tags). In one transaction it:
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
// delete.
//
// A retain_document job that is running while a retag lands adopts the new tags at its next batch (the
// batch reads them under the same Seq lock) instead of being superseded.
//
// # DocumentAdmin
//
//   - ListDocuments returns one keyset page, newest first (created_at, then id). page_size 0 means
//     [DefaultDocumentPageSize] (100), larger values are capped at [MaxDocumentPageSize] (1000), and a
//     negative size is domain.ErrInvalidArgument. tags_match defaults to any_strict and tags are normalized.
//     It asks the source for one row more than the page to learn whether a next page exists; the page
//     token is opaque base64url JSON of (created_at, id), and a malformed token is
//     domain.ErrInvalidArgument. An unknown or tombstoned bank is domain.ErrNotFound.
//   - GetDocument reads the document in a short transaction (tombstoned bank = domain.ErrNotFound) and,
//     only when asked, its original text from the blob store. A missing blob returns the document with
//     empty text; a read failure is domain.ErrUnavailable.
//   - ReprocessDocument delegates to the [Reprocessor], which enqueues a retain_document job that
//     re-extracts the document from its stored text (replacing its stored overrides when given), and then
//     returns that operation, read in a second short transaction.
//
// No method here calls a model, so every transaction is short; the Postgres Store.InTx re-runs a
// transaction up to 3 times in all after a deadlock (40P01) or serialization failure (40001), and every
// transaction body here re-reads its state, so a re-run is safe.
//
// # Tests
//
// retagger_test.go, retag_labels_test.go and retag_cancel_test.go run the retagger on fake.Store: the
// set-equal no-op, observation clearing on a real change, the job reading current tags, unknown documents,
// re-enqueue after a failed retag, label tags kept, and cancellation mid-transaction. documents_test.go
// covers listing, paging, reading with and without text, a missing blob, and reprocessing. SPEC
// "Behavioural rules" (UpdateDocument), "Jobs" (retag_document) and "gRPC surface" (DocumentService).
package docs
