// Package stub is a deterministic in-process stand-in for memd's gRPC API (Retain, Recall, Reflect,
// GetBankStats, ListOperations) so the eval harness and its targets can be tested end to end without
// Postgres or a model gateway. Recall is a keyword line match; Reflect echoes the best recalled lines.
package stub
