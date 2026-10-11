# 1005 — Invalidator.ClearScopes does not enqueue consolidation

## Context
`ClearObservations` takes a `reconsolidate` flag. If `Invalidator.ClearScopes` always enqueued the bank's
consolidate job, a clear with `reconsolidate=false` would still re-derive the scopes. The RPC also returns
the operation it started, which only its caller knows.

## Decision
Orchestrator ruling (review round 1, M5): `ClearScopes` deletes the scopes' observations and resets their
sources in the caller's tx (after `tx.Seq`), and enqueues nothing. The caller (WP-13's
ClearObservations) owns the enqueue and the returned operation.

`ClearForMemory` and `ClearDocumentUnits` keep enqueueing the automatic consolidate (DedupePending)
when they deleted or reset anything. Their callers (edit, invalidate, retag) always want
re-consolidation.

## Alternatives
- A `reconsolidate` parameter on ClearScopes. Rejected: it changes the spec's signature for a choice the
  caller already makes.

## Spec lines involved
ClearObservationsRequest.reconsolidate, ClearObservationsResponse.operation, Engines (Invalidator).
