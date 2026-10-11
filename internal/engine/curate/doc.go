// Package curate serves the observation admin RPCs (manual consolidation, scope listing, clears) and an
// observation's version history. Memory listing and curation live in internal/memory.
//
// # Where it sits
//
// curate is an engine package: it imports domain, the boundary packages and jobs and must never import
// adapters, server, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It declares what it consumes:
// [UnitSource] and [ScopeSource] (store.Reader methods) and [ObservationClearer] (consolidate.Invalidator); its
// jobs go through the jobs catalogue. cmd/memd builds [NewMemoryLister] (only its ObservationHistory is served,
// as GetObservationHistory) and [NewObservationAdmin], which ObservationService calls.
//
// # Observation history
//
// [MemoryLister.ObservationHistory] returns an observation's versions, newest first. An unknown or tombstoned
// bank is domain.ErrNotFound.
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
// Clears do not queue mental-model refreshes; affected models go stale through the change seq. Every
// transaction calls tx.Seq(bank) first; none calls a model.
//
// # Tests
//
// Tests run on a pgtest database, fake.Embedder and clock.Manual: paging, the observation admin RPCs, a clear
// leaving a pending rederive alone, and graph_maintenance after both clears. SPEC "gRPC surface"
// (ObservationService).
package curate
