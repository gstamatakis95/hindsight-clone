// Package ingest declares [Source], the provider-neutral boundary to a message queue that carries documents to
// retain (Kafka, SQS, Pub/Sub, NATS, ...), plus the [Message] a source delivers.
//
// # The contract
//
// A Source is one consumer's subscription with at-least-once delivery. Receive hands out messages, and every
// message ends in exactly one of three settlements: Ack (done, never seen again), Nack (try again no sooner than a
// delay) or DeadLetter (moved to the dead-letter destination, never seen again on the main subscription). A message
// that is never settled comes back: after Close, or after the provider's lease or visibility timeout runs out.
// Delivery is therefore at-least-once, and whoever consumes must be idempotent. memd is: a redelivered document
// retain is a no-op, joins the retain already in flight, or is stale (see retain.md).
//
// The rules every provider follows:
//
//   - Receive returns up to max messages, waiting at most the provider's long-poll time, and returns an empty slice
//     and no error when nothing arrived. A cancelled context returns its error; a closed source returns
//     [ErrClosed]. One goroutine calls Receive; Ack, Nack and DeadLetter may run from any goroutine, also while a
//     Receive waits.
//   - A message is settled through the Source that delivered it, with the message exactly as delivered (the
//     adapter's own token travels in Message.Handle, which the consumer never inspects).
//   - Settling is final and idempotent. Settling a message again, or after its lease expired or the source was
//     closed, is not an error and changes nothing: the first settlement wins, or the redelivery owns the message.
//   - Nack delays redelivery by at least the given duration. Messages with the same non-empty Key are delivered in
//     the order they were published, redeliveries included where the provider can keep that order; a provider
//     that cannot says so in its package comment. Messages with an empty Key have no order.
//   - DeadLetter needs a dead-letter destination. An adapter without one fails at construction, so nothing is ever
//     dropped silently.
//   - Failures are domain error classes: a broker or transport problem is domain.ErrUnavailable, wrapped with %w;
//     the provider error stays in the chain.
//
// # Providers
//
// internal/ingest/memq is the in-memory source used by tests. internal/ingest/kafka is the Kafka source (a
// consumer group, with the pure-Go franz-go client); its package comment has the commit model and the operational
// notes. internal/ingestapi is the consumer that drives a source; cmd/memd builds the source MEMD_INGEST_PROVIDER
// names (wire_ingest.go).
//
// Adding a provider (SQS, Pub/Sub, NATS):
//
//  1. Create internal/ingest/<provider> with a concrete type implementing Source and a compile-time assertion
//     (`var _ ingest.Source = (*T)(nil)`). Import only this package, internal/domain and the provider SDK, and add
//     the package to the adapters tier of .golangci.yml.
//  2. Map the provider's primitives to the four settlements. SQS maps Nack to ChangeMessageVisibility and
//     DeadLetter to send-to-DLQ then delete, with FIFO groups giving per-key order. Pub/Sub uses ModifyAckDeadline,
//     a dead-letter topic and ordering keys. NATS JetStream uses NakWithDelay and Term plus a publish to a DLQ
//     subject. Kafka has no nack: internal/ingest/kafka commits only contiguously settled offsets, redelivers a
//     nacked record locally after the delay (holding back only the later records of its key), and dead-letters
//     with a produce to the DLQ topic before settling the offset.
//  3. Run the shared contract suite from the adapter's tests: ingesttest.Run(t, func(t *testing.T)
//     ingesttest.Harness { ... }), giving every call a fresh subscription so tests never share messages.
//  4. Add the provider's settings to cmd/memd's Config (environment: endpoints, names and credentials are
//     infrastructure; behaviour such as poll waits goes in the server config file's ingest.consumer section), a
//     value to MEMD_INGEST_PROVIDER and a case to openSource in wire_ingest.go, and document the settings in the
//     README and docs/design/configuration.md.
package ingest
