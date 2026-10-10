# 1305 — Curation semantics: state/edit combinations, ordering, rederive skips, clears

## Context
SPEC "Curation" lists what edit, invalidate and restore commit in the RPC and what the rederive_memory job
does, but leaves open how `UpdateMemoryRequest.state` combines with edit fields, what a no-op request does,
when a rederive must not write, and how ClearObservations counts what it deleted.

## Decision
- **State and edits.**
  - `state = invalidated` with any edit field is `INVALID_ARGUMENT`: the unit is archived, so the edits would
    have nowhere to go.
  - `state = valid` on an archived unit restores it, then applies any edit fields in the same transaction.
    One rederive is enqueued, with one audit row per action (restore, edit).
  - `state = valid` on a live unit is no state change. With no edits it writes nothing and returns the unit
    with an empty operation id.
  - `state` unspecified with no edit field is `INVALID_ARGUMENT` ("nothing to update").
  - Other `INVALID_ARGUMENT` cases:
    - observations (they re-derive from their sources);
    - `fact_type` other than world or experience;
    - empty `text`;
    - `clear_occurred` combined with occurred dates.
- **Invalidate order.** The transaction runs these steps in order:
  1. `Seq`;
  2. read the live fact;
  3. `EvidenceRepo.DeleteCiting([unit])`, **before** `UnitRepo.Archive`, because Archive removes the evidence
     rows and the citing observations would otherwise survive;
  4. Archive;
  5. audit row;
  6. enqueue `consolidate` (`{}`, Pending) and `graph_maintenance` (`{}`, PendingOrRunning);
  7. one refresh per model whose `source_ids` include the unit or a deleted observation (CitingModels, then
     RefreshQueuer, deduplicated);
  8. the `memory_curated` event.

  The response is the pre-archive unit with state invalidated, no embedding, and no operation id.
- **Edit.** New values, `edited_at`, `seq` and a cleared embedding are written through `UnitRepo.Update`, which
  also clears the unit's consolidation stamps. `DeleteCiting([unit])` then deletes the citing observations and
  resets their co-sources. The edit enqueues no consolidation: the rederive does, once the unit has an
  embedding again. Entity names travel only in the rederive payload. `"entities": []` means clear and `null`
  means re-resolve the current names, so `RederivePayload.Entities` has no `omitempty`.
- **Rederive.** It reads and embeds outside any transaction. In its transaction it then:
  - takes `Seq`; a gone or tombstoned bank fails, and it never calls `Ensure`;
  - re-reads the unit. A unit that is no longer live (archived, or replaced by a reprocess) or whose text
    differs from the embedded text is left alone, and the job succeeds: a newer rederive is serialized
    behind it on `doc:{document}`;
  - otherwise writes the embedding, detaches and re-resolves the entities (`Literal` when
    `resolve_entities=false`), deletes the temporal and semantic links, re-links through the Linker and
    enqueues consolidate.

  Re-running it only redoes the same writes. When the config sets no entity match threshold it falls back to
  0.6.
- **Clears.**
  - `ClearObservations` counts `Deleted` from `ListScopes` *before* its transaction, because
    `consolidate.Invalidator.ClearScopes` returns no count. The figure is best effort under a concurrent
    consolidation.
  - `ClearMemoryObservations` counts exactly (`len(deleted)` from `DeleteCiting`).
  - Neither queues mental-model refreshes; the SPEC asks for direct refreshes on removals of units, not of
    observations.
  - Reconsolidation: `{}` with DedupePending, or the scopes payload with DedupeNone when scopes were given
    (decision 1301).
- **Wiring.** `CuratorDeps.Citing` and `Refresh` may be nil. Then the invalidate skips the direct refreshes,
  and models still go stale through the change sequence. WP-14 wires both.

## Alternatives
- Calling `consolidate.Invalidator.ClearForMemory` for edit and invalidate: rejected, because it hides the
  deleted observation ids the refresh lookup needs, and it pulls WP-10 into curation tests.
- Rejecting `state = valid` on a live unit: rejected; restoring something already live is idempotent.

## Spec lines involved
SPEC "Curation" table and bullets, "Behavioural rules" (no hard deletes), "Jobs" (rederive_memory serial key
`doc:{unit's document}`), decision 1301.
