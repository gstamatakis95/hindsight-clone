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

## Addendum: restore after graph maintenance pruned the unit's entities
Invalidate enqueues graph_maintenance, which usually runs before any restore. `Archive` queued the unit's
entities in entity_maintenance, so an entity only the archived unit referenced is pruned, and `Restore` then
skips it. To keep the round trip whole, a restore without new entities first reads the archived unit's
snapshot entity names through `CuratorDeps.Archived` (`UnitGetter`, i.e. `Reader.GetUnit` from CCR WP-13-1,
whose contract now says archived units return their snapshot names) and puts them in the rederive payload,
so the rederive re-resolves (re-creates) them. Test: `TestRestoreAfterEntityPruneKeepsEntities`.

## Addendum: review round 1
- **Clearing one memory's observations re-consolidates the memory itself.** ClearMemoryObservations rewrites
  the fact unchanged in the same transaction, keeping its embedding and taking the transaction's seq, through
  `UnitRepo.Update`, which clears consolidated_at and consolidation_failed_at. Observations are left alone.
  Test: `TestClearMemoryResetsOwnConsolidation`.
- **Rederive race.** When the text changed after embedding, the rederive still applies explicit payload
  entities (Detach, Resolve, Attach; no model call) and skips only the embedding write and the relink. A
  newer rederive whose payload has no entities would otherwise re-resolve the old names and lose the edit's.
  Test: `TestRederiveRaceKeepsEntities`.
- **graph_maintenance** (`{}`, serial `graph`, DedupePendingOrRunning) is enqueued by every curate path that
  removes units or detaches entities:
  - invalidate;
  - edit (citing observations deleted);
  - ClearObservations and ClearMemoryObservations;
  - rederive (Detach and link rebuild).

  Test: `TestRemovalPathsQueueGraphMaintenance`.
- **No consolidation on a NULL embedding.** Edit and restore stamp the unit consolidated
  (`MarkConsolidated`) in their transaction after `Update`/`Restore`. The rederive's `Update`, which writes
  the new embedding, clears the stamps, so the unit is consolidated again only once it has an embedding. The
  text-mismatch path writes no embedding and clears nothing; the newer rederive does both. Test:
  `TestEditKeepsUnitOutOfConsolidationUntilRederive` (edit and restore).
- **Citing-model lookup before the transaction.** Invalidate reads `ModelsCiting(b, [unit])` before its
  transaction, so no pool read runs while the transaction holds a connection. The CCR contract is "models
  whose source_ids include any of ids or any observation citing one of ids", which covers the observations
  the invalidate deletes. Inside the transaction it only calls `QueueRefresh` for each distinct model. A
  model that starts citing a new observation of the unit between the read and the commit is missed; it
  still goes stale through the change seq. The acceptance test's stub fails if it's called inside a
  transaction.
- **LOW items:**
  - An edit that sets an occurrence date clears `timeless` (`TestEditDateClearsTimeless`).
  - A rederive whose transaction fails after its context ended returns `domain.ErrCancelled`
    (`TestRederiveCancelledMidTx`).

## Addendum: clears own their consolidate enqueue (WP-10 review ruling)
`consolidate.Invalidator.ClearScopes` enqueues no consolidate. ClearObservations and ClearMemoryObservations
enqueue exactly one consolidate, only when reconsolidate=true, and return that operation; with
reconsolidate=false they enqueue none. Test: `TestObservationAdminClears`.

## Addendum: review round 2
- **Entity changes commit in the RPC's own transaction.** An edit's explicit names and a restore's
  archive-snapshot names are resolved inside the UpdateMemory transaction (Detach, Resolve, Attach;
  `EntityResolver.Resolve` makes no model call; the entity match threshold is read before `InTx` with the
  request overrides). The rederive payload carries only the unit id, and the rederive only re-embeds and
  relinks, keeping the attachments as committed. Reason: retries back off, so serial-key FIFO doesn't hold
  across a transient failure; an older rederive retried after a newer edit's rederive used to re-apply its
  stale names. The text-mismatch special case is gone: a mismatched rederive does nothing. The curator now
  takes `CuratorDeps.Resolver` and `CuratorDeps.Config`; the rederiver no longer does. Test:
  `TestRederiveBackoffRetryKeepsNewerEntities`.
- **ClearMemoryObservations leaves a fact waiting for its rederive alone** (no embedding): resetting its stamps
  would let consolidation decide on a NULL embedding; the rederive clears them when it writes the embedding.
  Test: `TestClearMemoryLeavesPendingRederiveAlone`.
- **M8 stranding risk.** Edit and restore keep the unit stamped consolidated until its rederive writes the
  embedding. If that rederive fails for good (FAILED after its attempts, or cancelled and never retried), the
  unit keeps a NULL embedding *and* a consolidated stamp, so nothing re-consolidates it and it stays out of
  semantic search until someone retries the operation or edits it again. Mitigations owned elsewhere: WP-15
  adds an adapter guard `embedding IS NOT NULL` on unconsolidated reads (so the stamp trick can later be
  dropped), and WP-14 adds a maintenance-tick reconcile that re-enqueues rederive for live facts with a NULL
  embedding and no pending rederive.
