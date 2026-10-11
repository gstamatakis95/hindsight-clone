// Package curate lists, inspects and curates individual memory units without hard deletes, runs the
// rederive_memory job, and serves the observation admin RPCs (manual consolidation, scope listing, clears).
//
// # Where it sits
//
// curate is an engine package: it imports domain, the boundary packages, jobs and retain (for retain.ResolveInput
// and retain.CauseMap) and must never import adapters, server, gen, pgx, net/http, the S3 SDK or Datadog
// (depguard). It declares what it consumes: [UnitSource], [UnitGetter], [CitingModels] and [ScopeSource]
// (store.Reader methods), [EntityResolver] and [Linker] (retain.EntityResolver and retain.Linker) and
// [ObservationClearer] (consolidate.Invalidator); its jobs, including the refreshes of the models an invalidate
// affects (jobs.QueueModelRefresh), go through the jobs catalogue. cmd/memd/wire.go builds [NewMemoryLister],
// [NewCurator], [NewObservationAdmin] and [NewRederiver]; the server's MemoryUnitService and ObservationService
// call the first three, and the rederive_memory job handler calls [Rederiver.Rederive].
//
// Observations are never curated directly; they re-derive from their sources. Curation never hard-deletes a
// fact: invalidating moves it to memory_units_archive, and restoring moves it back (SPEC "Curation").
//
// # Listing
//
// [MemoryLister.List] pages units newest first (created_at, then id) with filters on fact types, a text
// substring, document, entity (live units only), state (live, archived or both), a time range on a chosen
// timestamp, and tags. Page size 0 means [DefaultPageSize] (100); larger sizes are capped at [MaxPageSize]
// (1000). The page token is an opaque keyset cursor; a malformed token, an unknown fact type or state is
// domain.ErrInvalidArgument. No total is computed. [MemoryLister.Get] reads one unit, live or archived, and
// [MemoryLister.ObservationHistory] returns an observation's versions, newest first. An unknown or tombstoned
// bank is domain.ErrNotFound.
//
// # Curation: UpdateMemory
//
// [Curator.Update] takes an [UpdateMemoryRequest]. Validation before any transaction: state invalidated
// with edits, no state and no edits, a fact type other than world or experience, empty text, and
// clear_occurred together with dates are domain.ErrInvalidArgument. Then, still outside the transaction, it
// reads what the transaction will need: for a restore, the archived unit's snapshot entity names (graph
// maintenance may have pruned those entities while the unit was archived); for an entity change, the
// effective retain.entity_match_threshold with the request overrides (default 0.6); for an invalidate, the
// mental models citing the unit or an observation citing it. Then one DB.InTx:
//
// Invalidate (state invalidated):
//
//  1. tx.Seq(bank); read the live fact (absent = domain.ErrNotFound; an observation = INVALID_ARGUMENT);
//  2. delete the observations citing it and reset their co-sources (before the archive, which drops the
//     evidence rows);
//  3. UnitRepo.Archive, which snapshots its entities and causal links, deletes its links and entity rows and
//     queues its link neighbours in graph_dirty and its entities in entity_maintenance;
//  4. an invalidate audit row with the before snapshot and reason;
//  5. enqueue consolidate ({}, DedupePending) and graph_maintenance ({}, DedupePendingOrRunning) and one
//     refresh per citing mental model;
//  6. a memory_curated event stamped with the seq.
//
// No job follows; the response is the unit with state invalidated, no embedding and no operation id.
//
// Restore and/or edit (state valid, or edit fields):
//
//  1. tx.Seq(bank); with state valid, UnitRepo.Restore moves an archived unit back (reattaching the
//     entities and causal links that still exist) and stamps it consolidated; state valid on a live unit is
//     no state change, and with no edits nothing is written and the operation id is empty;
//  2. edits write the new values with edited_at, the seq and a cleared embedding (the unit leaves semantic
//     search until the rederive), stamp the unit consolidated, delete the observations citing it (resetting
//     their co-sources) and enqueue graph_maintenance; a new occurrence date clears Timeless;
//  3. entity changes are committed here, not in the job: the unit's attachments are detached and the new
//     names (or a restore's snapshot names) are resolved with the [EntityResolver] (no model call) and
//     attached; resolve_entities=false takes edited names literally;
//  4. enqueue rederive_memory (jobs.EnqueueRederive; jobs.RederivePayload: the unit id only; serial key
//     "doc:{document}" so it never overlaps a retain of that document, or "unit:{id}" for a unit without one;
//     DedupeNone; the request overrides ride on the job);
//  5. restore and edit audit rows (before and after snapshots) and the memory_curated event.
//
// Stamping an edited or restored unit consolidated keeps consolidation from deciding on it while its
// embedding is empty; the rederive's write clears the stamp. Resolving entities in the RPC transaction means
// an older rederive retried after a newer edit can never re-apply stale names. This deviates from SPEC
// "Curation", which lists entity re-resolution under the rederive job.
//
// # Rederive: rederive_memory
//
// [Rederiver.Rederive] reads the unit and embeds its text outside any transaction (no model call inside
// InTx), then in one short transaction: tx.Seq(bank) (a gone or tombstoned bank fails; it never creates a
// bank), re-read the unit, and do nothing if it is gone (archived or replaced) or its text changed since the
// embedding was computed (the newer edit's rederive is serialized behind it). Otherwise it writes the
// embedding with the seq (which clears the consolidation stamps), deletes and rebuilds the unit's temporal
// and semantic links through the [Linker], and enqueues graph_maintenance and consolidate. Entity
// attachments are left as the RPC committed them. A malformed payload is domain.ErrInvalidArgument; an
// error after ctx ended is domain.ErrCancelled. Running it twice only repeats the same writes.
//
// # Observation admin
//
//   - [ObservationAdmin.Consolidate] queues a manual consolidation. It is a write: Banks().Ensure, then
//     tx.Seq, then enqueue. Without scopes or overrides it is the automatic job ({}, DedupePending); with
//     either it carries a jobs.ConsolidatePayload and/or the overrides and uses DedupeNone
//     (jobs.EnqueueScopedConsolidate).
//   - [ObservationAdmin.ListScopes] pages the bank's observation scopes with counts, keyed by the scope's
//     canonical string; the shared (untagged) scope's empty key maps to the cursor "0", which sorts before
//     every tagged key, so paging past it never restarts the listing.
//   - [ObservationAdmin.ClearObservations] takes tx.Seq, deletes every observation of the scopes (all when
//     empty) and resets their sources through the [ObservationClearer], enqueues graph_maintenance and, when
//     reconsolidate is set, enqueues consolidation of the same scopes; [ClearResult] reports the count and the
//     operation.
//   - [ObservationAdmin.ClearMemoryObservations] does the same for the observations citing one live memory
//     (absent = domain.ErrNotFound) and also makes that fact consolidate again (rewrites it with the seq,
//     which clears its stamps), unless it has no embedding yet because its rederive is pending.
//
// Clears do not queue mental-model refreshes; affected models go stale through the change seq.
//
// # Invariants
//
// Every curation transaction calls tx.Seq(bank) first and stamps what it writes with that seq, so in-scope
// mental models go stale through the normal check. No transaction here calls a model; the Postgres
// DB.InTx re-runs a transaction up to 3 times in all after a deadlock (40P01), a serialization failure
// (40001) or an Attach of an entity that graph maintenance pruned meanwhile (a conflict), and the re-run
// resolves the names afresh. If every attempt loses that race, domain.ErrConflict surfaces (ABORTED).
// Every transaction body resets its outputs, so a re-run is safe.
//
// # Tests
//
// Tests run on a pgtest database, fake.Embedder and clock.Manual: paging, invalidate (citing observations removed,
// refreshes queued), restore round trip and restore after an entity prune, edits (embedding cleared,
// rederive queued, entities cleared, Timeless cleared, unit kept out of consolidation until the rederive),
// rederive skips (unit gone, text changed), a retried older rederive keeping newer entities, cancellation,
// graph_maintenance on every removal path, and the observation admin RPCs. SPEC "Curation", "Jobs"
// (rederive_memory) and "gRPC surface" (MemoryUnitService, ObservationService).
package curate
