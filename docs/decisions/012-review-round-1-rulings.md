# 012 — Job, event and config rulings from review rounds 1 and 2

## Context
Review round 1 of WP-00 found places where the spec is ambiguous or self-contradictory: the queue
semantics, event paging, model-binding merge and tag filters. The orchestrator ruled on each one
(orchestrator ruling, review round 1). Review round 2 replaced R8 and R10 with R8' and R10' and
added H3/H4 and a few contract fixes (orchestrator ruling, review round 2). This note records how
the contracts implement those rulings.
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

**R6 — Event paging** (amended by H4, review round 2)
- `EventRepo.List` and `ListEvents` never split a seq group, which is all the events of one transaction.
  The limit is soft: the page is extended to finish its last seq.
- `EventRepo.List` takes the `types` filter itself and returns `lastSeq`: the highest seq scanned,
  filtered-out events included, and never below `after_seq`. A filter that matches nothing still
  advances the poll.

**R7 — Worker fencing, without changing the spec's signatures**
- `Progress`, `Complete` and `Fail` act only on a PROCESSING row. On any other status they change
  nothing and return `ErrFailedPrecondition`. The one exception is R8's cancel-requested row.
- `Heartbeat` refreshes the jobs the worker still holds. It returns `*port.LostJobsError`, which wraps
  `ErrConflict`, naming the jobs it lost; the runner stops those handlers.
- `Alive` is true only while the row is PROCESSING without a cancel request and its bank is alive.
- **Known limit:** a worker whose job was reclaimed *and* re-claimed by another worker is fenced only
  through Heartbeat. Complete and Fail carry no worker id, so a write inside that window is possible.
  Closing the window needs a worker or attempt token in the signatures, which would be a spec change.

**R8' — Cancelling a PROCESSING job keeps its serial key** (replaces R8, review round 2)
- `Queue.Cancel` on a processing row records `cancel_requested_at` and sets `joinable = false`. The
  status stays PROCESSING, so the spec's claim SQL and unique processing index keep holding the serial
  key unchanged.
- `Alive` turns false, and `Heartbeat` keeps refreshing the row but names it in `*LostJobsError`. The
  handler stops at its next checkpoint.
- The handler's terminal `Complete` or `Fail` moves the row to CANCELLED (no event) and returns
  `ErrCancelled`. `ReclaimStale` moves a stale cancel-requested row to CANCELLED.
- A pending row becomes CANCELLED at once. `Retry` refuses every non-terminal row, a cancel-requested
  one included.
- Runner obligation: keep heartbeating an id until its handler returns, and on every exit (panic
  included) defer Complete or Fail.

**R9 — Tombstoned banks**
- `Queue.Enqueue` returns `ErrBankDeleting` except for `delete_bank`, and `Tx.Seq` already refuses the
  bank.
- `CancelBank` spares the `delete_bank` job.
- `BankRepo.DeleteBatch` removes content in batches. In the final transaction, `BankRepo.Delete` removes
  the bank row with its remaining events and operations (including the delete_bank row itself; the
  runner treats `ErrNotFound` from its Complete as done). A recreated bank therefore starts clean.

**R10' — Terminal failures** (replaces R10, review round 2; the finalizer is gone)
- The runner owns the attempt cap: at attempt+1 >= `MaxAttempts` it calls `Fail(nil)`. The Claimer never
  caps a `Fail` with retryAt. Consolidate keeps its unbounded, capped-backoff retry (SPEC 1950).
- `Fail(nil)` writes FAILED and appends one OPERATION_FAILED event under a fresh `Tx.Seq`.
- `ReclaimStale` past max attempts does the same: FAILED with exactly one event. On a tombstoned or gone
  bank the row still becomes FAILED, without the event, and the sweep never fails because of one bank.
- Refresh pause and REFRESH_FAILED history (SPEC 2003) come from an idempotent reconcile in
  `RunMaintenance` (WP-05 / WP-12). It finds models whose latest refresh operation is FAILED and that
  are not yet paused or recorded. The Claimer never touches mental-model tables.

**Other round-2 fixes**
- **H3.** `BankRepo.DeleteBatch` and `Delete` return `ErrFailedPrecondition` unless `deleting_at` is
  set. `DeleteBatch`'s doc lists every bank-keyed table. The deleter re-checks the tombstone before
  `Blob.DeletePrefix`.
- **Refresh dedupe.** A refresh_mental_model Enqueue with `DedupeNone` is treated as `DedupePending`.
- **Self-continuations** (consolidate, graph_maintenance and delete_bank re-enqueuing leftover work)
  use `DedupePending`.
- **Rederive support.** `UnitRepo.Update` and `SetDocumentTags` clear `consolidated_at` and
  `consolidation_failed_at`; the old observations go through `EvidenceRepo.DeleteCiting`.
  `LinkRepo.DeleteForUnit` and `EntityRepo.Detach` let rederive rebuild links and entities.
- **Restore** skips snapshot entities and link partners that no longer exist, and stamps the
  transaction's seq.
- **Adapter obligations** are one-line doc notes and become WP-01 acceptance items:
  - InTx runs at READ COMMITTED.
  - The dedupe advisory lock is taken in its own statement before the lookup, with keys in sorted
    order when one tx enqueues several (or a retry on 40P01).
  - A joined row is locked FOR UPDATE.
  - The bank row is locked FOR KEY SHARE on Enqueue.

**Write-side ports added now** because the spec clearly requires them (decision 005 lists the rest):

| Port | Methods |
|---|---|
| `Tx.Directives()` / `DirectiveRepo` | Directive RPCs |
| `UnitRepo` | `Update` (curation edit, embedding cleared), `SetDocumentTags` (retag, SPEC 1923), `AppendAudit` (SPEC 1963), `RecordObservationVersion` (SPEC 1971) |
| `EntityRepo` | `PendingMaintenance`, `PruneOrphans` (SPEC 1925), `Detach` (round 2) |
| `LinkRepo` | `Count`, `Dirty`, `ClearDirty` (SPEC 1925), `DeleteForUnit` (round 2) |
| `BankRepo` | `DeleteBatch` (SPEC 1926) |
| `Queue` | `Get`, `List`, `Cancel`, `Retry`, `Prune` (OperationService, SPEC 1845, 2010-2015) |
| `EventRepo` | `Prune` |

## Alternatives
- Add a worker token to Complete and Fail. Rejected for now: it changes the spec's signatures.
- Fail refresh jobs in ReclaimStale and reconcile pauses later from the maintenance tick. Rejected: a
  second path that is harder to test.

## Spec lines involved
80, 503, 714, 908, 1219, 1230, 1845, 1921-1926, 1947-1955, 1963, 1971, 1980, 2005, 2010-2015, 2034.
