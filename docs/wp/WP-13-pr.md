# WP-13 · Bank, curation, documents, maintenance engines — PR

Branch `wp/WP-13-bank-curate-docs-maint`, rebased on main (99fbdd1). Plan: `docs/wp/WP-13.md`.
Gates: `make gen` (no diff), `make lint BREAKING_AGAINST=contracts-v1` (0 issues), `make test` (race) pass.

## Built
- [x] Bank engines
  - [x] templates: `NewTemplateCodec(store, ModelSpecReader, RefreshQueuer)`; Export (no memories; ids
        cleared), Create, and new `CreateNamed` (name + first-build operations, one tx)
  - [x] directives: `NewDirectiveSelector(store)` (global + tag-overlap; `all` = every active),
        `NewDirectiveAdmin(store)` (satisfies `server.DirectiveAdmin` as is)
  - [x] BankDeleter: `NewBankDeleter(store, blob, JobFence, clock, cfg)`. Tombstone = MarkDeleting +
        CancelBank + enqueue delete_bank (PendingOrRunning) in one tx; Run = batched DeleteBatch under a batch
        budget, then Alive + tombstone re-check right before `Blob.DeletePrefix`, then `BankRepo.Delete`; a run
        that finds the bank gone or recreated deletes no blobs
  - [x] stats via indexed counts: `NewStatsReader(StatsSource)` with keyset tag paging
  - [x] prompt preview: `NewPromptPreviewer(ConfigResolver, DirectiveSelector)`, every PromptOperation,
        chunks mode skipped, config blocks with field/section/active
  - [x] `TenantAdmin`, `BankAdmin` (`NewTenantAdmin`, `NewBankAdmin`, patch inside the write tx)
- [x] Curation
  - [x] MemoryLister (both tables via `UnitSource`, keyset pagination, opaque tokens, stale via the source)
  - [x] Curator: edit / invalidate / restore with the Curation table's in-tx effects (invalidate:
        DeleteCiting before Archive, audit, event, consolidate + graph_maintenance + refresh of citing models)
  - [x] Rederiver (embed outside the tx; re-embed, relink, enqueue consolidate; entity changes commit in the
        UpdateMemory tx; skips when the unit is gone or its text changed; never Ensure)
  - [x] `ObservationAdmin` (Consolidate, ListScopes, ClearObservations, ClearMemoryObservations)
- [x] Retagger: `Retag` (set-equal = no-op, no seq bump; tags committed on the document, job deduped Pending) and
      `Apply` (the retag_document job; re-reads current tags); `DocumentAdmin` (list, get with blob text,
      reprocess via `Reprocessor`)
- [x] GraphMaintainer over `graph_dirty` and `entity_maintenance`, 240 s budget on the injected clock, batches
      committed one tx each, done only after a final empty check, handler re-enqueues on done=false

## Acceptance tests
| Accept | Test | Result |
|---|---|---|
| Invalidate: recall stops returning the citing observations immediately; citing models get a refresh queued | `curate.TestInvalidateRemovesCitingObservationsAndQueuesRefresh` | pass |
| Restore round-trip | `curate.TestRestoreRoundTrip`, `curate.TestRestoreAfterEntityPruneKeepsEntities` (graph maintenance pruned the entity while archived) | pass |
| Bank delete: queued jobs never resurrect data, writes fail during the delete, a write afterwards creates a fresh bank | `bank.TestBankDeleteLifecycle` | pass |
| Retag no-op | `docs.TestRetagSetEqualIsNoop` | pass |
| Graph maintenance resumes after its budget runs out | `maintenance.TestGraphMaintenanceResumesAfterBudget` | pass |

Supporting tests: bank `TestTombstoneIdempotent`, `TestDeleteRunSkipsPrefixWhenBankGone`,
`TestDeleteRunStopsWhenCancelled`, `TestTemplateRoundTrip`, `TestTemplateCreateRollsBackOnBadSpec`,
`TestDirectiveSelect`, `TestDirectiveAdmin`, `TestTenantAdmin`, `TestBankAdmin`, `TestTagsPaging`, `TestStats`,
`TestPreview`, `TestPreviewChunksSkipsAndUnknownOp`; curate `TestEditClearsEmbeddingAndQueuesRederive`,
`TestEditClearEntities`, `TestCuratorRejectsObservations`, `TestRestoreLiveUnitIsNoop`,
`TestRederiveSkipsWhenUnitGone`, `TestRederiveSkipsWhenTextChanged`, `TestListerPaging`,
`TestObservationAdmin{Consolidate,ListScopes,Clears,ClearMemory}`; docs `TestRetagChangeClearsObservations`,
`TestRetagJobReadsCurrentTags`, `TestRetagUnknownDocument`, `TestDocumentAdminList`,
`TestDocumentAdminGetAndReprocess`; maintenance `TestGraphMaintenanceTopsUpAndPrunes`,
`TestGraphMaintenanceStopsOnTombstone`.

## Decisions and CCRs
- CCR `docs/ccr/WP-13-1.md` (PROPOSED): Reader methods GetUnit, ListUnits, ObservationHistory, BankStats,
  TagCounts, ListDocuments, ListScopes, MentalModelSpecs, ModelsCiting, BanksToConsolidate; `domain.Unit.Stale`.
- 1300 consumer reads mirroring the CCR; 1301 job payloads / serial keys / dedupe; 1302 server admin types
  (WP-14 aliases `ConfigPatch`, `ListDocumentsRequest`, `DocumentPage`, `DocumentView`, `ScopePage`,
  `ClearResult`; switches CreateBankFromTemplate to `CreateNamed`); 1303 refresh and reprocess delegated via
  `RefreshQueuer` / `Reprocessor`; 1304 graph maintenance; 1305 curation rules; 1306 bank engines; 1307 docs.

## For WP-14
- Wire `store.Read()` as `UnitSource`, `curate.UnitGetter` (CuratorDeps.Archived), `CitingModels`, `ScopeSource`, `StatsSource`, `ModelSpecReader`,
  `DocumentSource` and `worker.ConsolidationCandidates` once CCR WP-13-1 lands.
- Provide `RefreshQueuer` (WP-12) and `Reprocessor` (WP-08); `consolidate.Invalidator` as
  `curate.ObservationClearer`; `retain.EntityResolver` + a ConfigResolver for the curator, `retain.Linker` for the rederiver and maintainer;
  `port.Claimer` as `bank.JobFence`.
- Handlers: delete_bank and graph_maintenance re-enqueue themselves (DedupePending) on done=false;
  rederive → `Rederiver.Rederive`; retag → `Retagger.Apply`. WP-10's consolidate handler must accept
  `curate.ConsolidatePayload`.

## Known gaps
- Every CCR-backed read (listings, stats, tags, scopes, export specs, citing models, stale) is tested only against
  in-package stubs until CCR WP-13-1 is implemented in Postgres and the fake.
- `ClearObservations.Deleted` is best effort (counted from a scope listing before the clear;
  `consolidate.Invalidator.ClearScopes` returns no count).
- A removal that enqueues graph_maintenance (PendingOrRunning) between the maintainer's last empty check and its
  Complete joins the finishing job; its queue rows stay and drain on the next trigger (decision 1304).
- Narrow fencing window between the tombstone re-check and `Blob.DeletePrefix` (Alive doesn't check the
  worker id); a retain blob `Put` racing a tombstone can leave an orphan blob (retain should check the bank
  before its Put).
- Rederive falls back to an entity match threshold of 0.6 when the effective config sets none: server defaults
  (internal/config/defaults.go) don't set `retain.entity_match_threshold` yet, so the default lives in curate.
  WP-04/WP-14 should set it in ServerDefaults (and WP-08 should use the same value).
- The restore keeps pruned entities only when `CuratorDeps.Archived` is wired (CCR WP-13-1 `GetUnit`).
- Invalidate's direct refreshes are skipped when `Citing` / `Refresh` aren't wired; models then go stale only
  through the change sequence.

## Review round 1 fixes
| Item | Fix | Test |
|---|---|---|
| H1 bank delete vs processing jobs | `finish` checks for other PROCESSING jobs of the bank before the blob delete and again in the `Banks().Delete` tx; if any, enqueues a delayed continuation (`DeleteBlockedDelay`) and returns done=false | `bank.TestBankDeleteLifecycle` (extended) |
| H2 ClearMemoryObservations resets the memory's own stamps | `Units().Update` (embedding kept, tx seq) in the same tx | `curate.TestClearMemoryResetsOwnConsolidation` |
| H3 stale entities from a retried older rederive | round 2: edit / restore names commit in the RPC's own tx; payload carries only the unit id; rederive only re-embeds and relinks (mismatch special case removed) | `curate.TestRederiveBackoffRetryKeepsNewerEntities` |
| M4 graph_maintenance from every removal path | edit, both clears, rederive, retag Apply enqueue it. The maintainer already reported done only from an empty batch after the last working one; `TestGraphMaintenanceRechecksAfterFinalBatch` is a regression guard for that, not a fix. The join-at-finish re-check (work enqueued between the final empty check and Complete) goes to the WP-14 handler. | `curate.TestRemovalPathsQueueGraphMaintenance`, `docs.TestRetagChangeClearsObservations`; guard: `maintenance.TestGraphMaintenanceRechecksAfterFinalBatch` |
| M5 per-page blob fencing | optional `DeleterConfig.Blobs` (`BlobLister`, CCR WP-13-2) deletes page by page with Alive + tombstone checks between pages; fallback `DeletePrefix` | `bank.TestDeletePagesBlobsWithFencing` |
| M6 re-issued DeleteBank resumes | documented (1306); WP-14: delete_bank retries without an attempt cap | `bank.TestRedeleteResumesHalfDeletedBank` |
| M7 completion signal | GetBank NOT_FOUND is the completion signal (1306, Tombstone doc) | doc |
| M8 no consolidation on a NULL embedding | edit/restore re-stamp consolidated_at; rederive's embedding write clears it | `curate.TestEditKeepsUnitOutOfConsolidationUntilRederive` |
| M9 ModelsCiting before the tx | read before InTx; CCR contract now includes observations citing the ids | `curate.TestInvalidateRemovesCitingObservationsAndQueuesRefresh` (stub fails inside a tx) |
| M10 retag no-op after a failed/cancelled retag | re-enqueue when the newest retag op of the doc is failed/cancelled | `docs.TestRetagSetEqualAfterFailedRetagReenqueues` |
| WP-10 ruling: clears own the consolidate enqueue | exactly one consolidate only when reconsolidate=true | `curate.TestObservationAdminClears` |
| LOW | Timeless cleared on a date edit; Apply no-op only for a gone document; mid-tx cancel -> ErrCancelled (rederive, retag, maintainer); missing blob -> empty text | `curate.TestEditDateClearsTimeless`, `curate.TestRederiveCancelledMidTx`, `docs.TestRetagUnknownDocument`, `docs.TestGetDocumentMissingBlobIsEmptyText` |

## Review round 2 fixes
| Item | Fix | Test |
|---|---|---|
| HIGH H3 (backoff breaks FIFO) | entity edits and restore snapshot names resolved in the UpdateMemory tx (threshold read before InTx); rederive payload is `{unit_id}`; rederive only re-embeds and relinks | `curate.TestRederiveBackoffRetryKeepsNewerEntities` |
| MEDIUM resetOwnConsolidation regression | returns early when the fact has no embedding (pending rederive) | `curate.TestClearMemoryLeavesPendingRederiveAlone` |
| Docs | M8 stranding risk recorded (1305, gaps below); M4 row reworded | — |
| LOW retag gone bank | Apply wraps a gone bank (Tx.Seq NOT_FOUND) as ErrCancelled | `docs.TestRetagUnknownDocument` |
| LOW tests | in-tx re-check before `Banks().Delete`; retag cancelled mid-tx | `bank.TestDeleteFinalTxRechecksProcessingJobs`, `docs.TestRetagApplyCancelledMidTx` |

New CCR: `docs/ccr/WP-13-2.md` (`Blob.List`). Remaining gaps: fencing window is one blob page (whole
`DeletePrefix` when no lister is wired); a blocked final tx after blobs were deleted can leave a retain blob
`Put` from the still-running job; **M8 stranding**: a rederive that fails for good leaves the fact with a NULL
embedding and a consolidated stamp (out of semantic search and consolidation until retried or re-edited). An
adapter guard `embedding IS NOT NULL` on unconsolidated reads lands in WP-15 and a tick reconcile in WP-14;
the graph_maintenance join-at-finish re-check belongs to the WP-14 handler.
