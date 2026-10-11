# 1453 — Settling retain_batch parents

## Context
SPEC: "Large retains create a retain_batch parent with one retain_document child per document; the parent's
status aggregates its children." The ingestor enqueues the parent as a PENDING operation. No worker runs it
(`Register` never handles retain_batch), and nothing in the adapters or engines ever changed its status. So
the parent stayed PENDING forever:
- `BankStats.pending_operations_by_kind` never emptied after an async multi-document retain, so the barrier
  hung. This was the first failure of `e2e.TestScenarios/RetainAsync`.
- Pending rows are never pruned.

## Decision
- **The query.** A new Postgres adapter method, `(*postgres.Store).SettleRetainBatches(ctx)`, is one
  idempotent UPDATE (`queries/settle.sql`). It ends every PENDING retain_batch whose children are all
  terminal:
  - FAILED with "k of n documents failed" when a child failed;
  - else CANCELLED when a child was cancelled;
  - else COMPLETED.

  Its result holds the counts per status. Children are found through `operations_parent_idx`.
- **The tick step.** The worker's maintenance tick runs it as step `settle_retain_batches`, through
  `worker.BatchSettler`, narrowed in `wire.go`. A parent therefore settles within one tick after its last
  child. The barrier waits that long. For an eval barrier the spec recommends sync Retain, which creates no
  parent.
- **Not a port method.** It is a worker-only maintenance query, like `StrandedFacts` (decision 1452).

## Alternatives
- **A retain_batch handler that waits for its children.** Rejected: it holds a worker slot per batch.
  Enough concurrent batches would deadlock against their own children.
- **Settling in the child's Complete transaction** (adapter claimer). Rejected: it changes the frozen
  Claimer contract's documented effects, and it would need fake and porttest changes too.
- **Computing the parent's status at read time.** Rejected: the row would still count as pending in
  BankStats and would never be pruned.

## Known limits
- A child retried after its parent settled does not reopen the parent.
- The parent carries no usage; its children do.

## Spec lines involved
SPEC "Behavioural rules / Operations" (retain_batch, eval barrier), "Jobs" table (retain_batch row).
