// Package stub is a deterministic in-process stand-in for the part of memd's gRPC API the eval uses (Retain, Recall,
// Reflect, GetBankStats, GetBankConfig, ListOperations), so the eval harness and its targets can be tested end to end,
// and the eval command's -stub flag can smoke-test a run, without Postgres, a model gateway or a memd process. It says
// nothing about memd's answer quality.
//
// # Usage
//
//	addr, stop, err := stub.Start(stub.Options{PendingPolls: 2}) // serves on a 127.0.0.1 port
//	defer stop()
//	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
//	tgt := target.NewMemd(conn, "tenant", "org")
//
// [Start] serves a new [Server] on a loopback port and returns the address and a stop function that waits for the
// server to exit; [New] and [Server.Register] put the stub on a caller's *grpc.Server. The stub implements the
// generated memoryv1 MemoryService, BankService, ConfigService and OperationService server interfaces; every other
// RPC returns UNIMPLEMENTED.
//
// # Behaviour
//
// Every call needs the x-tenant-id metadata ([TenantHeader]; missing is INVALID_ARGUMENT), and banks are kept per
// (tenant, bank). Reading an unknown bank is NOT_FOUND; Retain creates it.
//
//   - Retain upserts each item as a document by document_id (a generated id when empty), reports EXTRACT usage of 20
//     input and 4 output tokens per item, arms the bank's pending polls and records one COMPLETED consolidate operation
//     with CONSOLIDATE usage.
//   - GetBankStats reports the document count and, for the next Options.PendingPolls calls after a Retain, one pending
//     consolidate in pending_operations_by_kind, which exercises the eval barrier.
//   - Recall scores every stored line by how many distinct query words (letters and digits, at least 3 runes,
//     case-insensitive) it contains and returns up to 10 lines with a positive score, best first, ties in document and
//     line order.
//   - Reflect answers with the top 3 recalled lines joined by newlines, or "I don't know.", and reports REFLECT usage
//     of 100 input and 20 output tokens.
//   - GetBankConfig reports an effective config with EXTRACT, CONSOLIDATE and REFLECT bound to the stub's model, for
//     the run metadata.
//   - ListOperations returns the bank's recorded operations in one page, filtered by status and kind; Options.FailedOps
//     seeds that many FAILED consolidate operations into each new bank.
//
// The server is safe for concurrent use; [Server.Retains] counts the items retained across all banks.
//
// # Testing
//
// server_test.go covers these rules; eval/harness, eval/target and eval/golden use the stub in their own end-to-end
// tests.
package stub
