// Package memq is an in-memory ingest.Source for tests and the contract suite's self-test. A [Queue] is the broker:
// tests Publish to it and read its DeadLetters; Queue.Source opens a consumer. It keeps strict per-key order,
// including across Nack, and redelivers messages whose lease ran out. It stores nothing durably, so it is not a
// production source.
package memq
