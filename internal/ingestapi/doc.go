// Package ingestapi is the queue consumer that `memd ingest` runs: it reads an ingest.Source, retains each message
// through memory.Service.RetainBatch (the path the RetainBatch RPC shares) and settles every message from its
// result. Like internal/grpcapi it is a transport: it decodes, calls the memory service and maps the outcome, and
// holds no retain logic of its own.
//
// # Messages
//
// A message is one memory.v1.IngestEnvelope: a document (RetainItem, document_id required) for one tenant, org and
// bank, with optional document tags, overrides and skip_consolidation. The body is protojson unless the message's
// content-type attribute is application/x-protobuf; unknown JSON fields are errors. Producers set the message key to
// the document id, so the versions of one document arrive in order, and set item.revision so a redelivered older
// version is STALE instead of overwriting a newer one.
//
// Config.Tenant pins the source to one tenant: an envelope without tenant_id is that tenant's, and one naming
// another tenant is dead-lettered (TENANT_MISMATCH). Config.Org pins the org the same way (ORG_MISMATCH). An
// unpinned source requires tenant_id in every envelope and trusts it, which is only safe when the broker's publish
// permissions are the tenant boundary.
//
// # The loop
//
// Run receives up to Config.BatchSize messages, polling again while messages keep coming until the batch is full or
// Config.FlushInterval has passed. It decodes each message (an undecodable or invalid one is dead-lettered at
// once), groups the rest by bank and request-level fields into RetainBatch calls of at most Config.BatchSize items
// and Config.MaxRequestBytes of content, runs up to Config.Concurrency calls at once, and settles each message
// from its item's result (decide):
//
//	ACCEPTED, UNCHANGED, STALE, SUPERSEDED          Ack, after the write transaction committed
//	REJECTED, or a permanent call error             DeadLetter with the error's reason
//	ORG_DELETING; ORG_NOT_FOUND with a receipt      Ack and drop (EventDropped): a deleted org's content stays gone
//	RESOURCE_EXHAUSTED (INGEST_BACKLOG)             Nack after the RetryInfo delay, and pause receiving for it
//	FAILED, a transient error, BANK_DELETING,       Nack after an exponential backoff; DeadLetter (MAX_ATTEMPTS)
//	ORG_NOT_FOUND without a receipt                 once the delivery count reaches Config.MaxAttempts
//
// The error class decides before the status: an org or bank deletion error can arrive as a REJECTED item. A dead
// letter that fails is nacked instead, so a message is never held in flight. Delivery is at-least-once end to end:
// nothing is acked before its commit, and a crash between commit and Ack redelivers a message that RetainBatch then
// finds unchanged, joins in flight or finds stale, so it never queues a second extraction.
//
// # Shutdown
//
// When Run's context ends it stops receiving, retains and settles the messages it already holds (under a context
// that outlives the signal by Config.ShutdownGrace), then closes the source. Whatever is still unsettled after the
// grace is redelivered later.
//
// # Observability
//
// Hooks carries two optional callbacks, set by cmd/memd from internal/observability: Batch wraps every RetainBatch
// call (a span carrying the tenant, org and bank, and the batch latency metric) and Count counts messages per event
// with a bounded reason code (EventReceived, EventAcked, EventNacked, EventDeadLettered, EventDropped). The package
// itself imports no vendor library.
//
// Design: docs/design/retain.md#queue-ingestion.
package ingestapi
