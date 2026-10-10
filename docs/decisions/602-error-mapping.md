# 602 — Domain error to gRPC status mapping

## Context
`domain/errors.go` names a gRPC code for most error classes. `ErrCancelled` and `ErrConflict` have none,
and the spec doesn't cover context errors or unclassified errors.

## Decision
`toStatus` checks these classes in order with `errors.Is`. The first match wins.

| Error | Code |
|---|---|
| `context.DeadlineExceeded` | DEADLINE_EXCEEDED |
| `context.Canceled` | CANCELED |
| `ErrNotFound` | NOT_FOUND |
| `ErrAlreadyExists` | ALREADY_EXISTS |
| `ErrInvalidArgument` (incl. `ErrTooManyScopes`) | INVALID_ARGUMENT |
| `ErrFailedPrecondition` (incl. `ErrBankDeleting`) | FAILED_PRECONDITION |
| `ErrUnavailable` | UNAVAILABLE |
| `ErrResourceExhausted` | RESOURCE_EXHAUSTED |
| `ErrCancelled`, `ErrConflict` (incl. `*port.LostJobsError`) | ABORTED |
| anything else | INTERNAL |

- **Context errors first.** An engine that wraps a context timeout as `ErrUnavailable` still reports
  DEADLINE_EXCEEDED.
- **Existing statuses pass through.** An error that already carries a gRPC status is returned unchanged.
- **Messages.** The message is the error text.
- **Where it's called.** Every handler calls `toStatus` on every error path. There is no mapping
  interceptor, so wrapcheck sees every error converted.

## Alternatives
- **CANCELLED for `ErrCancelled`.** Rejected: in gRPC, CANCELLED means the *caller* cancelled. An
  operation cancelled through CancelOperation, or a bank deleted under a sync retain, is an abort.
- **Hiding unclassified messages.** Rejected for v1: there is no auth or PII scope, and the text helps
  debugging.

## Spec lines involved
domain/errors.go comments, gRPC surface ("unknown bank = NOT_FOUND", deleting bank FAILED_PRECONDITION),
Behavioural rules (strict failure, `max_total_tokens` → RESOURCE_EXHAUSTED).
