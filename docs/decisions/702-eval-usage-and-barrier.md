# 702 — Eval token usage comes from what the API exposes; the barrier needs two idle polls

## Context
The report must show token usage per slot. memd exposes usage on `ReflectResponse.usage` and on
`Operation.usage` (every background job), but a synchronous `RetainResponse` carries no usage. Stock
Hindsight reports a single `usage` total on retain and reflect responses, not per slot, and its operations
carry none. The spec defines the eval barrier as "use sync Retain and poll
`BankStats.pending_operations_by_kind` until it is empty".

## Decision
- memd usage = Reflect responses + the usage of every operation of the bank (`ListOperations`, all statuses,
  read after the bank's questions) + the judge (`JUDGE`). Sync-retain EXTRACT tokens appear only if the server
  records them on an operation the API lists; the harness doesn't estimate them.
- Hindsight usage is reported under the slots `RETAIN` (retain totals) and `REFLECT` (reflect totals).
- Barrier: poll `GetBankStats` every `-poll` (1 s) until `pending_operations_by_kind` is empty on two consecutive
  polls (Hindsight's `waiting.py` rule: one idle poll can land between a job completing and the next being
  enqueued), bounded by `-settle-timeout` (15 min, also Hindsight's figure). A timeout fails that conversation's
  questions with an error naming what was still pending; the run continues.
- Failed background operations don't stop the run. They are counted in the report (`failed_operations`).

## Alternatives
- Async retain so EXTRACT usage lands on `retain_document` operations. Rejected: the card asks for sync Retain.
- Fail the conversation on a failed operation (Hindsight does). Rejected: a benchmark run of hours should keep
  going; the count is in the report.

## Spec lines involved
SPEC "Behavioural rules / Operations" (eval barrier); "Model routing" (usage accounting); `BankStats`,
`Operation.usage`, `ReflectResponse.usage` in the proto.
