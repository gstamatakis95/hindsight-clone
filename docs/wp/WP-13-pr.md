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
  - [x] Rederiver (embed outside the tx; re-resolve, relink, enqueue consolidate; skips when the unit is gone or
        its text changed; never Ensure)
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
  `curate.ObservationClearer`; `retain.EntityResolver` / `retain.Linker` for the rederiver and maintainer;
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
