# 1452 — Re-queue rederive for facts stranded with a NULL embedding

## Context
An edit or restore clears a fact's embedding and keeps its consolidation stamp until rederive writes the new
embedding (decision 1305). A rederive that fails for good strands the fact: it drops out of semantic search
and consolidation (WP-13 known gap M8). The carried item is a maintenance tick step that re-enqueues
rederive for such facts. No port read lists them across banks, and rederive is DedupeNone with a serial key
that covers the whole document, so queue dedupe can't stop a tick from piling up jobs.

## Decision
- **The read.** A new Postgres adapter method, `(*postgres.Store).StrandedFacts(ctx, retryAfter, limit)`,
  backed by the sqlc query `queries/stranded.sql`. It is not a port method. The worker consumes it through
  its own `worker.StrandedFactSource`, and `wire.go` narrows the store to it. This is the "adapter query"
  option WP-05 left open for `ConsolidationCandidates`.
- **What counts as stranded.** A live fact (not an observation) whose embedding is NULL, on a bank that is
  not tombstoned, and whose rederive_memory operation ended FAILED or CANCELLED. It is skipped while any
  rederive of the unit is pending or processing, or ended within `retryAfter` (default 10 min). A fact
  whose rederive keeps failing is therefore retried every 10 minutes, not every tick.
- **Starting from operations.** The query starts from the operations table (index on status and kind)
  instead of scanning `memory_units` for NULL embeddings, so the per-minute tick stays cheap on large
  banks. Operation rows are pruned after 7 days. A stranded fact is re-queued within minutes, so it keeps a
  fresh operation row. Only a deployment that is down for the whole retention period can lose track of one.
- **The tick step** `rederive_stranded` enqueues the rederive in curate's shape: payload `{"unit_id"}`,
  serial key `doc:{document}` or `unit:{id}`, DedupeNone. Up to 100 per tick. Two workers ticking at once
  may queue a unit twice; rederive is idempotent.

## Alternatives
- **A CCR for a port read.** Rejected: a frozen-path change for one maintenance query. This read, like
  `SettleRetainBatches` (decision 1453), only serves the worker.
- **Scanning memory_units for `embedding IS NULL`.** Rejected: there is no index on it, so it would be a
  full scan every minute.

## Spec lines involved
SPEC "Curation" (edit and restore wait for rederive), "Jobs" (maintenance tick); decisions 1305 and 502.
