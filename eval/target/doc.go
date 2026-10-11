// Package target holds the eval's harness.Target implementations: [Memd] talks to memd over gRPC and [Hindsight] talks
// to a stock Hindsight server over its HTTP API, so both can run the same benchmark and their reports read side by
// side.
//
// # Memd
//
// [NewMemd] wraps any grpc.ClientConnInterface; every call carries the tenant in the x-tenant-id metadata header, as
// memd requires.
//
//	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
//	tgt := target.NewMemd(conn, "eval")
//
// Its methods:
//
//   - Retain sends one synchronous RetainRequest with one item (document id = session id, the session time as
//     timestamp, unset when zero) and returns the per-slot usage the response reports.
//   - PendingOperations reads GetBankStats.pending_operations_by_kind, dropping zero counts; an empty map is the eval
//     barrier's "idle".
//   - Recall and Reflect pass the question's query time and return the recalled texts in result order, or the reflect
//     text and its usage.
//   - Background pages ListOperations (100 per page, retain_batch parents excluded so their children's usage is not
//     counted twice), sums usage per slot and counts FAILED operations; a repeating page token is an error.
//
// Slot names drop memd's MODEL_SLOT_ prefix (EXTRACT, REFLECT, ...), in first-seen order.
//
// # Hindsight
//
// [NewHindsight] targets the HTTP API under {baseURL}/v1/default/banks/{bank}; a non-empty API key is sent as a bearer
// token. Retain posts one item to /memories synchronously and reports Hindsight's single usage total under RETAIN;
// Recall posts to /memories/recall with the query timestamp; Reflect posts to /reflect (Hindsight's reflect takes no
// query time) and reports usage under REFLECT; PendingOperations counts pending and processing operations by task type
// (at most 100 of each status); Background reports only the number of failed operations, because Hindsight's operations
// carry no usage. Non-2xx responses are errors quoting up to 512 bytes of the body; response bodies are read up to 32
// MiB.
//
// # Testing
//
// memd_test.go runs Memd against the in-process eval/stub server (round trips, background usage, tenant requirement and
// isolation); hindsight_test.go runs Hindsight against httptest servers that check the request paths and bodies.
package target
