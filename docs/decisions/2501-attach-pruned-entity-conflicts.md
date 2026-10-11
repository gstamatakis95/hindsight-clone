# 2501 — Attach of a pruned entity is a conflict, and the transaction re-resolves

Status: accepted (WP-25)

## Context
graph_maintenance's `PruneOrphans` takes no bank seq, so it can delete an orphan entity after a retain
batch (or a curation entity change) has resolved a mention to it but before `Attach` runs. This happens, for
example, during a reprocess: `replace()` queues entity_maintenance and graph_maintenance, and the graph job
runs while the retain's next batch is in flight. Postgres `Attach` locked the entities and silently skipped
the missing ones. The new unit lost the entity, and the job reported success.

## Decision
- Postgres `Attach` returns `errEntityGone` when fewer entities lock than were requested, and attaches
  nothing. `errEntityGone` wraps `domain.ErrConflict` ("a concurrent writer won a race; the caller may
  retry").
- `Store.InTx` re-runs a transaction that fails with `errEntityGone`, as it does for a deadlock (up to
  `txAttempts`). Retain resolves entities inside its batch transaction, after `tx.Seq`, and curation
  resolves them inside its own transaction. So the re-run's `Candidates` no longer sees the pruned entity
  and creates or merges into a live one.
  - If every attempt loses, `ErrConflict` surfaces. A job retries it, since it is not permanent. A sync
    Retain or UpdateMemory returns ABORTED, which the client may retry.
- The fake's `Attach` returns `domain.ErrConflict` for a missing entity, where it used to return
  ErrNotFound. Its transactions are serialized, so this only matters for consistency with Postgres. The
  frozen `EntityRepo.Attach` doc is unchanged; `porttest:EntitiesAttachPrunedIsConflict` pins the
  behaviour.
- `ErrNotFound` was not used. The worker treats it as "bank gone" in several places, so it would end the
  job quietly, which is the same loss under another name.

## Alternatives
- **Prune under `tx.Seq(b)`.** This would close the window, because candidates are read inside retain's
  Seq-locked transaction. But graph maintenance deliberately takes no seq: entity rows are not memory
  content, so models must not go stale over them. It would also hold the bank lock across a batch of up to
  600 link top-ups and prunes, blocking every retain of the bank.

## Tests
`postgres.TestAttachAfterConcurrentPruneReresolves`, `porttest:EntitiesAttachPrunedIsConflict`.
