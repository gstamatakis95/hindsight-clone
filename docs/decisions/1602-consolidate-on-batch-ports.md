# 1602 — Consolidation and the Invalidator on the batch ports

## Context
WP-10 declared `ObservationCounter`, `ScopeLister` (`Scopes`), `ModelLister`, `ObservationClearer` and
`DocumentScopeReader` because the ports lacked those reads and deletes. CCR batch #6, #7, #8, #20, #21 and
#22 added them. The port's `DeleteScopes` deletes nothing for an empty list; "all" is the separate
`DeleteAllObservations`.

## Decision
- **Deleted stand-ins:** `ObservationCounter`, `ModelLister`, `ObservationClearer` and
  `DocumentScopeReader`, together with the `ConsolidatorDeps.Counter`, `.Models` and `.DocScopes` fields.
  - `capacity` calls `Store.Read().CountObservations`. It runs in the decide phase, outside any
    transaction, and counts committed observations as the old counter did. So
    `max_observations_per_scope` no longer needs a wired dependency.
  - `refreshCandidates` calls `Store.Read().ListMentalModels(ModelQuery{Bank, RefreshAfterConsolidation:
    true})` with PageSize 0 (all). It then drops paused models.
  - On a tombstoned bank this is NotFound. Run already fails the same way earlier, at `Config.Effective`.
- **`ScopeLister`** is now the port method `ListScopes(b, after, limit)`, satisfied by `port.Reader`.
  - Preview asks for `limit+1` scopes; `Complete` is `len <= limit`.
  - Each `ScopeInfo.Scope` maps to `NewScopeKey(b, tags)`.
- **`NewInvalidator()` takes no arguments.**
  - Every method calls `tx.Seq` first, before any read or delete.
  - `ClearDocumentUnits` uses `tx.Units().IDsByDocument`. `ClearForMemory` and `ClearDocumentUnits` use
    `tx.Evidence().CitingScopes`, then `DeleteCiting`.
  - `ClearScopes` maps an empty list to `DeleteAllObservations` and a non-empty one to `DeleteScopes`. It
    returns the deleted observation ids, so ClearObservations reports `len(deleted)` (WP-13).
  - Only `ClearScopes` can reach the delete-all path. No data-derived list is ever passed to it.
    `TestInvalidatorDataDerivedEmptyNeverClearsAll` covers the data-derived paths, and
    `TestInvalidatorSeqBeforeDelete` pins the call order.

## Alternatives
- Counting inside the apply transaction (`tx.CountObservations`): the cap is decided before the LLM call,
  outside any transaction, so it would need a transaction just for the count. Rejected.

## Spec lines involved
SPEC 63, 254 and 290 (max_observations_per_scope); 693-702 (ClearObservations, Preview `complete`); 1486-1488
(Invalidator); CCR batch §4 WP-10.
