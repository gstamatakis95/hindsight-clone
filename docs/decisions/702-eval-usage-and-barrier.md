# 702 — Where eval token usage comes from, the barrier, and per-call timeouts

## Context
The report must show token usage per slot. memd reports usage in three places:
- `RetainResponse.usage`, for a synchronous retain. This field was added by CCR WP-07-1.
- `ReflectResponse.usage`.
- `Operation.usage`, on every background job.

Stock Hindsight reports a single `usage` total on its retain and reflect responses, not split per slot, and
its operations carry no usage. The spec defines the eval barrier as "use sync Retain and poll
`BankStats.pending_operations_by_kind` until it is empty".

## Decision
- **memd usage** is the sum of four sources:
  - sync Retain responses (EXTRACT, TIME_PARSE, EMBED, ...);
  - Reflect responses;
  - the usage of every operation of the bank, read with `ListOperations` across all statuses, parents excluded;
  - the judge, under the `JUDGE` slot.
- **Background usage is collected whenever the bank exists.** That means at least one Retain succeeded, even
  when a later retain or the barrier failed. So a failed conversation still reports what it cost and how many
  of its jobs failed.
- **Hindsight usage** is reported under two slots: `RETAIN` (retain totals) and `REFLECT` (reflect totals).
- **Barrier.** The harness polls `GetBankStats` until `pending_operations_by_kind` is empty on two consecutive
  polls. This is Hindsight's `waiting.py` rule: a single idle poll can land between one job completing and the
  next being enqueued.
  - Defaults: `-poll` 1 s and `-settle-timeout` 15 min (also Hindsight's figure).
  - For `-dataset golden` the defaults are 200 ms and 90 s, so CI against `memd all` fails fast.
  - A timeout fails that conversation's questions with an error naming what was still pending. The run
    continues.
- **Per-call timeout.** Every single target and judge call (retain, stats poll, recall, reflect, judge,
  operations listing) is bounded by `-call-timeout`, default 5 min, where 0 means no bound. A hung RPC then
  fails one question instead of the whole run.
- **Failed background operations** don't stop the run. They are counted in the report as `failed_operations`.

## Alternatives
- Use async retain so that EXTRACT usage lands on `retain_document` operations. Rejected: the card asks for
  sync Retain, and CCR WP-07-1 added the response field instead.
- Fail the conversation on a failed operation, as Hindsight does. Rejected: a benchmark run that takes hours
  should keep going, and the count is in the report.

## Spec lines involved
- SPEC "Behavioural rules / Operations" (eval barrier).
- SPEC "Model routing" (usage accounting).
- In the proto: `BankStats`, `Operation.usage`, `ReflectResponse.usage` and `RetainResponse.usage`
  (CCR WP-07-1).
