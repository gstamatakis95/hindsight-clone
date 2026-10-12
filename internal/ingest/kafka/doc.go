// Package kafka implements ingest.Source on a Kafka topic, read by a consumer group with the pure-Go franz-go
// client (no cgo). It works with any broker that speaks the Kafka protocol: Apache Kafka, Redpanda, Amazon MSK,
// Confluent Cloud.
//
// # Where it sits
//
// The package is the Kafka adapter of internal/ingest. It imports internal/ingest, internal/domain and franz-go,
// and nothing else of memd; nothing outside it imports franz-go. cmd/memd does not wire this source yet: the
// consumer loop that drives a Source (decode the envelope, retain, settle) is what will build it from the
// settings below.
//
// # Usage
//
//	src, err := kafka.New(kafka.Config{
//		Brokers: []string{"kafka-1:9092", "kafka-2:9092"},
//		Topic: "memd-ingest", Group: "memd-ingest", DeadLetterTopic: "memd-ingest-dlq",
//	})
//	defer src.Close()
//	msgs, err := src.Receive(ctx, 100)
//	// retain them, then settle each one:
//	err = src.Ack(ctx, done...)
//	err = src.Nack(ctx, 30*time.Second, retryLater...)
//	err = src.DeadLetter(ctx, "undecodable", broken...)
//
// [New] validates the [Config] and starts joining the group in the background; it does not wait for the brokers,
// so a broker outage shows up at Receive or at a commit, not at construction. A missing broker list, topic, group
// or dead-letter topic, or a dead-letter topic equal to the topic, is domain.ErrInvalidArgument naming the field.
//
// # Configuration
//
// Every Config field is a plain value; the package reads no environment and no files. Once cmd/memd wires the
// source, the settings split the way memd's configuration does:
//
//   - Environment (deployment, infrastructure and secrets): Brokers, Topic, Group and DeadLetterTopic, the TLS
//     material (CA, client certificate and key, built into Config.TLS) and the SASL mechanism, user and password.
//     These name the infrastructure and carry its credentials, like the S3 and database settings.
//   - The server config file (behaviour): PollWait and MaxBuffered, next to the consumer's batch size, concurrency,
//     attempts and backoff.
//
// # Delivery
//
// Delivery is at-least-once, as the ingest contract requires. Every record a Receive hands out ends settled by
// Ack, Nack or DeadLetter, or is redelivered: after Close, a crash or a rebalance, the next owner of its
// partition reads again from the group's committed offset. There is no lease: a record handed out and never
// settled stays in flight while this consumer holds the partition (the contract suite's lease case does not
// apply).
//
// Message.ID is "topic/partition/offset"; Message.Key is the record key, Message.Body its value and
// Message.Attributes its headers (the last value wins for a repeated header). Message.Attempt counts the
// deliveries of the record by this consumer since it was assigned the partition. Kafka keeps no delivery count, so
// it starts again at 1 after a rebalance or a restart: it is a lower bound, good enough to stop retrying a record
// that keeps failing.
//
// A new group starts at the earliest offset of every partition, so nothing published before the first consumer
// started is skipped. Records of aborted transactions are never delivered (read-committed isolation).
//
// # Ordering
//
// Kafka orders records per partition only, and the default partitioner sends one key to one partition, so
// producers set the record key to the document id: all versions of a document then arrive in publish order.
// The source keeps that order per key, through Nack too: a record with a non-empty key is not delivered while an
// earlier record of its key is in flight or waiting out a Nack delay, so one key never has two records open at
// once. Records with an empty key have no order. Different keys of one partition flow independently.
//
// # Nack
//
// Kafka has no negative acknowledgement, so Nack is local: the record becomes deliverable again, by this
// consumer, once the delay has passed, ahead of every later record of its key. The other keys of the partition
// keep flowing meanwhile; nothing seeks back and nothing is refetched. The record still counts as unsettled, so
// the commit cannot pass it (see below) and a rebalance or a restart in the meantime redelivers it from the broker.
//
// # The commit model
//
// Kafka commits one offset per partition, meaning "everything before this offset is done". The source commits by
// hand (auto-commit is off) and only past records that are settled contiguously: for each partition it keeps the
// fetched records in offset order, pops the settled prefix and commits the offset after the last popped record.
// The offset is that record's offset plus one, never a count, because offsets have gaps (transaction markers,
// compacted records). An Ack of a later record therefore commits nothing until every earlier record of its
// partition is settled too; then one commit covers them all.
//
// Ack and DeadLetter commit synchronously, before they return, whatever became committable. Commits are
// serialized and only move forward. A commit failure is domain.ErrUnavailable, but the settlement stands: the next
// Ack, DeadLetter, revoke or Close commits the offset, and a crash before that only redelivers settled records,
// which memd's retain path absorbs (a redelivered document is unchanged, joins the retain in flight, or is stale).
//
// # Head of line
//
// The price of per-partition offsets: a record that keeps failing holds back the commit of its whole partition
// while it waits out its Nacks, even though later records of other keys are acked. A crash or a rebalance during
// that time redelivers those acked records as well. The source also keeps every record behind the unsettled head
// in memory until the head settles; once a partition holds Config.MaxBuffered records (default 10 000) the source
// pauses fetching it, which blocks every key of that partition until the head is acked or dead-lettered, and
// resumes it at half. The consumer bounds this: it dead-letters a record after its maximum attempts.
//
// # Rebalances and Close
//
// A consumer never commits for a partition it no longer owns: polls block rebalances until the polled records are
// tracked, and on a revoke the source commits the revoked partitions' watermarks, then forgets them, so their
// outstanding messages settle nothing (settling them is not an error, as the contract requires). A lost partition
// (the member was evicted) is forgotten without a commit.
//
// Close commits every watermark, forgets every partition and leaves the group, so the partitions move to the
// other members at once rather than after the session timeout; the unsettled records are redelivered to them.
// Receive after Close returns ingest.ErrClosed. The group member is dynamic (no static instance id), so a restart
// is a leave and a join.
//
// # Dead letters
//
// DeadLetter produces a copy of the record to Config.DeadLetterTopic: the same key and value, the original
// headers, and the failure headers [HeaderReason], [HeaderTopic], [HeaderPartition], [HeaderOffset] and
// [HeaderAttempt] (headers with [HeaderPrefix] on the original are replaced, so a re-published dead letter carries
// only its latest failure). It settles the record only once the brokers acknowledged the produce; a produce
// failure is domain.ErrUnavailable and leaves the record in flight, to be dead-lettered again or nacked. A
// rebalance between the produce and the settlement redelivers a record that already reached the dead-letter topic,
// so a dead letter can appear twice.
//
// # Operations
//
//   - Partitioning: key records by document id and give the topic at least as many partitions as memd ingest
//     processes, since a partition is read by one member of the group at a time. Adding partitions later moves
//     keys to other partitions, which breaks per-key order for the documents in flight at that moment.
//   - The dead-letter topic must exist, with at least the topic's retention: the client never asks the brokers to
//     auto-create it, so a missing one fails every DeadLetter with domain.ErrUnavailable after a few metadata
//     retries. It holds full document content, so its retention is also how long a deleted org's content can survive
//     there: set it to what the deployment's data-deletion policy allows, and replay or purge it from there.
//   - Errors: a broker or transport failure is domain.ErrUnavailable with the franz-go error in the chain;
//     retriable fetch errors are retried by the client and never surface.
//   - Lag: consumer-group lag (the end offset minus the committed offset per partition) is the backlog measure.
//     memd exports no lag metric yet; read it from the brokers' tooling (kafka-consumer-groups, rpk) meanwhile.
//
// # Testing
//
// kafka_test.go starts one Apache Kafka broker per test binary (testcontainers, the apache/kafka-native image, a
// single KRaft node) and runs the shared contract suite (internal/ingest/ingesttest) on a fresh two-partition
// topic and group per case, plus the commit-model and dead-letter header checks. Without Docker the tests skip,
// or fail when MEMD_REQUIRE_DOCKER is set.
package kafka
