// Package ingesttest is the contract suite every ingest.Source adapter runs from its own tests: [Run] takes a
// constructor that returns a [Harness] over a fresh, empty subscription and checks the behaviour the ingest package
// comment specifies (redelivery of unsettled messages, Nack delay, dead letters, per-key order, idempotent
// settlement and Receive's edge cases). It is test support, not a fake: production code never imports it.
package ingesttest
