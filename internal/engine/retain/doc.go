// Package retain serves ReprocessDocument ([Reprocessor]): it re-queues retain over a stored document. The
// retain pipeline itself (the Retain RPC and the retain_document job) lives in internal/memory.
//
// # Where it sits
//
// retain is an engine package: it imports domain, the boundary packages and jobs and must never import
// adapters, server, gen, pgx, net/http, the S3 SDK or Datadog (depguard). docs reuses its [Reprocessor];
// cmd/memd builds it.
//
// # Reprocess
//
// [Reprocessor.Reprocess] makes no model call. In one transaction it takes tx.Seq(bank) first (which locks
// the bank and fails on a tombstoned one), reads the document (unknown = domain.ErrNotFound), replaces its
// stored overrides when given, rebuilds a forced jobs.RetainPayload from the stored blob key, tags and item
// scopes, and the context, metadata, mention time and timeless flag of the newest never-curated unit (entity
// hints are not stored and are lost), re-upserts the document and enqueues retain_document
// (jobs.EnqueueRetainDocument: serial key "doc:{id}", DedupeNone). Re-extraction replaces the units, which
// resets their curation.
//
// # Tests
//
// Tests run on a pgtest database seeded through the store: the payload (scopes, context, metadata, mention
// time, forced, stored or new overrides), an unknown document and a deleting bank, and a curated unit never
// spreading its values. SPEC "gRPC surface" (DocumentService.ReprocessDocument).
package retain
