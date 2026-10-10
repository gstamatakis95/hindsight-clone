# 012 — Job, event and config rulings from review round 1

## Context
Review round 1 of WP-00 found places where the spec is ambiguous or self-contradictory: the queue
semantics, event paging, model-binding merge and tag filters. The orchestrator ruled on each one
(orchestrator ruling, review round 1). This note records how the contracts implement those rulings.
The port doc comments in `internal/port/jobs.go` and `repos.go` are normative. The porttest suites
assert each rule.

## Decision

**R1 — Dedupe and overrides** (SPEC 908/1219/2034 vs 1924/2005)
- A job with overrides is never deduplicated, in either direction, for every kind except
  `refresh_mental_model`.
- A refresh always joins its model's queued refresh (pending, including parked), whatever the
  overrides. The caller then calls `Queue.Upgrade`.
- Joinability is stored on the row as `operations.joinable`. A row is joinable only when it was enqueued
  with a dedupe policy and no overrides; refresh rows are always joinable.
- A row inserted with `DedupeNone` is never joined. That covers consolidate jobs that carry overrides or
  scopes (SPEC 1921).
- Dedupe enqueues are serialized per (tenant, bank, kind, serial key) for the rest of the transaction.
  In Postgres this is `pg_advisory_xact_lock`. Concurrent leaderless enqueuers therefore never both
  insert.

**R2 — Upgrade merges under the row lock** (SPEC 1230/2005)
- Payloads are JSON objects and are shallow-merged:
  - a present key replaces the queued value;
  - a boolean is OR-ed, so `force_full` is never cleared;
  - an absent key is kept.
- Non-nil overrides replace the queued ones; nil keeps them.
- Strategy and overrides are therefore replaced only when the joiner supplies them. Payload producers
  omit unset optional fields.
- Upgrade applies to PENDING rows only.

**R3 — Reschedule** releases or parks a PENDING row only. On PROCESSING or terminal rows it returns
`ErrFailedPrecondition`. A worker returns a claimed job only through `Fail(retryAt)`.

**R4 — ModelBinding merge** (SPEC 80)
- Bindings merge per slot. The most specific level that sets a slot supplies the whole binding.
- Unset fields inside a binding mean the gateway or adapter default. They are not inherited from a less
  specific level's binding for the same slot.
- WP-04's `Merge` implements this.

**R5 — tags vs tag_groups** (SPEC 503, 714)
- On Recall and Reflect, setting both `tags` and `tag_groups` is INVALID_ARGUMENT
  (`domain.TagFilter.Validate`).
- Only `RefreshTrigger.tag_groups` (and `ResolvedScope`) override a model's tags.
- `domain.MatchFilter` ANDs both parts and never drops one.
- `MatchGroup` and `port.SQLPredicate.Eval` share one semantics:
  - an empty all_of / and is true;
  - an empty any_of / or is false;
  - a malformed node matches nothing.

**R6 — Event paging**
- `EventRepo.List` and `ListEvents` never split a seq group, which is all the events of one transaction.
  The limit is soft: the page is extended to finish its last seq.
- `last_seq` is the highest seq in the page, or the request's `after_seq` when the page is empty. It is
  never 0 unless `after_seq` was 0.

**R7 — Worker fencing, without changing the spec's signatures**
- `Progress`, `Complete` and `Fail` act only on a PROCESSING row. On any other status they change
  nothing and return `ErrFailedPrecondition`. The one exception is R8's cancelled row.
- `Heartbeat` refreshes the jobs the worker still holds. It returns `*port.LostJobsError`, which wraps
  `ErrConflict`, naming the jobs it lost; the runner stops those handlers.
- `Alive` is true only while the row is PROCESSING and its bank is alive.
- **Known limit:** a worker whose job was reclaimed *and* re-claimed by another worker is fenced only
  through Heartbeat. Complete and Fail carry no worker id, so a write inside that window is possible.
  Closing the window needs a worker or attempt token in the signatures, which would be a spec change.

**R8 — Cancelling a PROCESSING job keeps its serial key**
- `Queue.Cancel` on a processing row sets CANCELLED with `finished_at` NULL. The handler's terminal
  `Complete` or `Fail` sets `finished_at`, keeps the row CANCELLED and returns `ErrCancelled`.
  `ReclaimStale` finishes stale cancelled rows.
- While the handler winds down, `Heartbeat` keeps refreshing the cancelled row, so the key stays held for
  as long as the worker lives. It also reports the row in `*LostJobsError`, so the runner cancels the
  handler's context. Only a dead worker's cancelled row goes stale, and `ReclaimStale` then finishes it.
- A pending row is finished at once.
- The claim's NOT EXISTS treats `status = 'cancelled' AND finished_at IS NULL` like PROCESSING. The full
  amended claim SQL is in decision 004.

**R9 — Tombstoned banks**
- `Queue.Enqueue` returns `ErrBankDeleting` except for `delete_bank`, and `Tx.Seq` already refuses the
  bank.
- `CancelBank` spares the `delete_bank` job.
- `BankRepo.DeleteBatch` removes content in batches. In the final transaction, `BankRepo.Delete` removes
  the bank row with its remaining events and operations (including the delete_bank row itself; the
  runner treats `ErrNotFound` from its Complete as done). A recreated bank therefore starts clean.

**R10 — Terminal failures**
- `Fail(nil)` and `ReclaimStale` past max attempts write FAILED and append an OPERATION_FAILED event
  under a fresh `Tx.Seq`. The event is skipped when the bank is tombstoned or gone.
- Refresh bookkeeping (pause, REFRESH_FAILED history) belongs to the handler. Kinds with
  `domain.OperationKind.HasFinalizer()` (only `refresh_mental_model`) are not failed by `ReclaimStale`.
  They return to PENDING with `Attempt = MaxAttempts`, and the runner hands a job claimed at that
  attempt to the kind's finalizer. The finalizer writes its bookkeeping and calls `Fail(nil)`.

**Write-side ports added now** because the spec clearly requires them (decision 005 lists the rest):

| Port | Methods |
|---|---|
| `Tx.Directives()` / `DirectiveRepo` | Directive RPCs |
| `UnitRepo` | `Update` (curation edit, embedding cleared), `SetDocumentTags` (retag, SPEC 1923), `AppendAudit` (SPEC 1963), `RecordObservationVersion` (SPEC 1971) |
| `EntityRepo` | `PendingMaintenance`, `PruneOrphans` (SPEC 1925) |
| `LinkRepo` | `Count`, `Dirty`, `ClearDirty` (SPEC 1925) |
| `BankRepo` | `DeleteBatch` (SPEC 1926) |
| `Queue` | `Get`, `List`, `Cancel`, `Retry`, `Prune` (OperationService, SPEC 1845, 2010-2015) |
| `EventRepo` | `Prune` |

## Alternatives
- Add a worker token to Complete and Fail. Rejected for now: it changes the spec's signatures.
- Fail refresh jobs in ReclaimStale and reconcile pauses later from the maintenance tick. Rejected: a
  second path that is harder to test.

## Spec lines involved
80, 503, 714, 908, 1219, 1230, 1845, 1921-1926, 1947-1955, 1963, 1971, 1980, 2005, 2010-2015, 2034.
