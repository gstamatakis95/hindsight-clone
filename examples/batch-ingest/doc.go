// Command batch-ingest shows bulk ingestion from a producer with at-least-once delivery: RetainBatch with a
// document id and a revision on every item, a status per item, a redelivered batch that writes nothing, an
// out-of-order old version that is ignored, and the backlog rejection a producer backs off from.
package main
