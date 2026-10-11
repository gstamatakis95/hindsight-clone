# WP-16 PR: engine follow-ups (wire engines to the batch ports)

Branch: `wp/WP-16-engine-followups`, rebased on main (WP-14a merged). Ruling 1a = A.

## Built
- [x] **WP-08 retain**
  - `Upsert` sets `Document.ObservationScopes` from the item. `DocumentScopesWriter` and
    `IngestorDeps.ScopesWriter` are deleted.
  - The no-op hash (`NoopHash`) was already identical at both sites, mixing in the scopes only when they are
    non-nil. It is unchanged, and the async path now has a test.
  - `replace` lists the document's units with `IDsByDocument`. Decision 802 is marked superseded.
  - **New `retain.Reprocessor`** (`NewReprocessor(port.Store)`), which satisfies `docs.Reprocessor`. Nothing
    implemented it before. It re-upserts the document carrying `ObservationScopes`, replaces the overrides
    only when new ones are given, and enqueues a forced `retain_document` (decision 1600).
- [x] **WP-09 recall**
  - `TagVocabulary` is the `TagCounts` method, sorted by count descending, then tag.
  - `RecallerDeps.Lookups` is removed: `Reader` serves fuzzy tags, chunks and entity state.
    `NewIncluder(r, count)`.
  - `Memory.Stale` comes from one `StaleScopes` call over the packed observations' scopes.
  - The temporal arm sets `Embedding = q.Embedding` and `MinSimilarity = 0.1`.
  - `recall.FuzzyTagMinSimilarity(cfg)` is exported for WP-12.
- [x] **WP-10 consolidate**
  - Deleted: `ObservationClearer`, `ObservationCounter`, `ModelLister`, `DocumentScopeReader`, and the
    `ConsolidatorDeps.Counter`, `.Models` and `.DocScopes` fields.
  - `NewInvalidator()` takes no arguments. It calls `tx.Seq` first, then uses `tx.Units().IDsByDocument` and
    `tx.Evidence().CitingScopes`.
  - `ClearScopes` maps empty to `DeleteAllObservations` and non-empty to `DeleteScopes`, and now returns the
    deleted ids.
  - The cap uses `Read().CountObservations`. Refresh candidates come from
    `Read().ListMentalModels(ModelQuery{Bank, RefreshAfterConsolidation: true})`.
  - `ScopeLister` is `ListScopes(b, after, limit)`, with Preview's `complete = len <= limit`.
  - `unitMode` reads `doc.ObservationScopes` first. Decision 1003 is updated.
- [x] **WP-11 reflect**
  - `isContextOverflow` is `errors.Is(err, domain.ErrContextOverflow)`.
  - `ChunkReader` is deleted; expand reads chunks with `port.Reader.GetChunks`. `DefaultTools(rc, pages, r)`.
- [x] **WP-12 mentalmodel**
  - `ScopeQuery`, `ModelQuery`, `CronModel` and `FailedRefresh` are aliases of the port types.
  - Renamed `NewestSeqInScope` and `ListMentalModels`.
  - `ScopeReader` also has `TagCounts`, so fuzzy leaves resolve to exact bank tags through recall's compiler
    before the scope compiles. Decision 1203 is updated.
  - The agentic refresh sets `ReflectRequest.Context` (the rendered `mm_refresh_context`), and the TODO is
    resolved.
  - The test harness reads through the fake's `port.Reader`. Only the CronModels and FailedRefreshes lists
    remain test-only.
- [x] **WP-13 curate/docs/bank**
  - `UnitQuery`, `UnitCursor`, `DocumentQuery` and `DocumentCursor` are aliases of the port types.
  - `ModelSpecReader` is deleted. `NewTemplateCodec(s, refresh)` exports specs via `ListMentalModels`.
  - ClearObservations reports `Deleted = len(deleted)`, and the pre-count pager is removed.
  - The ListScopes RPC maps a last key of `""` to the `"0"` cursor.
  - The `ScopeSource`, `CitingModels`, `UnitSource`, `StatsSource`, `DocumentSource` and `BlobLister` docs
    now point at the port.
- [x] **Gateway:** overflow markers are matched against the full capped body. The 512-byte snippet is kept
  for the message only.
- [x] **Porttest**
  - `IDsByDocument` and `CitingScopes` on an unknown bank and on a tombstoned empty bank.
  - A discriminating EntityStates id-order test.
  - `MarkConsolidationFailed` clears a tagged scope in `StaleScopes`.

## Acceptance tests
Every item runs against `internal/fake`. The porttest additions run against both the fake and Postgres.

| Item | Test |
|---|---|
| WP-08 Upsert stores item scopes, nil stays nil, scope-only change re-runs | `retain.TestIngestStoresItemScopes` |
| WP-08 same no-op hash at both sites (async path, with and without scopes) | `retain.TestRetainAsyncScopesNoop` |
| WP-08 replace via IDsByDocument (observations deleted, co-sources reset) | `retain.TestRetainReplacementDeletesObservationsAndResetsCoSources` |
| WP-13 reprocess carries `ObservationScopes`, overrides replayed or replaced, item context kept | `retain.TestReprocessCarriesScopesAndOverrides` |
| Reprocess: unknown document is NotFound, deleting bank is BankDeleting | `retain.TestReprocessUnknownDocumentAndDeletingBank` |
| WP-09 TagCounts vocabulary (count desc, then tag) | `recall.TestBankTagsMostUsedFirst`, `recall.TestRecallFuzzyTagsThroughFake` |
| WP-09 Memory.Stale from StaleScopes | `recall.TestRecallMemoryStale` |
| WP-09 temporal Embedding and MinSimilarity 0.1 | `recall.TestTemporalArmRelevanceFilter` |
| WP-09 include chunks and entities via GetChunks and EntityStates | `recall.TestIncludeChunksTruncated`, `recall.TestRecallIncludes` |
| WP-10 Seq before DeleteScopes / DeleteAllObservations (call order) | `consolidate.TestInvalidatorSeqBeforeDelete` |
| WP-10 a data-derived empty list never reaches DeleteAllObservations | `consolidate.TestInvalidatorDataDerivedEmptyNeverClearsAll` |
| WP-10 CitingScopes, IDsByDocument, ClearScopes returns deleted | `consolidate.TestInvalidatorClearForMemory`, `consolidate.TestInvalidatorClearScopesAndDocument` |
| WP-10 unitMode reads doc.ObservationScopes | `consolidate.TestRunItemScopesBeatOverrides` |
| WP-10 ListMentalModels refresh candidates | `consolidate.TestRunTriggersRefreshes`, `consolidate.TestRunRefreshSurvivesFailedRun` |
| WP-10 CountObservations cap | `consolidate.TestRunObservationCap` (now on the fake's reader; the "needs a counter" branch is gone) |
| WP-10 ListScopes Preview | `consolidate.TestStrategyPreview`, `consolidate.TestStrategyPreviewIncomplete` |
| WP-11 ErrContextOverflow | `reflect.TestIsContextOverflow`, `reflect_test.TestReflectContextOverflowSynthesizes` |
| WP-11 expand chunks via port.Reader | `reflect_test.TestToolExpand` |
| WP-12 refresh context set | `mentalmodel.TestAgenticRefreshSetsContext` |
| WP-12 fuzzy leaves resolved via TagCounts | `mentalmodel.TestFuzzyScopeResolvesBankTags` |
| WP-12 NewestSeqInScope and ListMentalModels on the fake | the whole mentalmodel suite (harness uses `store.Read()`) |
| WP-13 export via ListMentalModels | `bank.TestTemplateRoundTrip` |
| WP-13 ClearObservations deleted = len(deleted) | `curate.TestObservationAdminClears` |
| WP-13 ListScopes page size 1 terminates (`""` maps to `"0"`) | `curate.TestListScopesPageSizeOneTerminates`, `curate.TestObservationAdminListScopes` |
| Gateway marker past the 512-byte snippet | `gateway_test.TestContextOverflowClassified/marker_past_the_512-byte_snippet` |
| Porttest: IDsByDocument and CitingScopes, unknown and tombstoned bank | `TestReadsContract/error_regimes` (fake + Postgres) |
| Porttest: EntityStates id order (names reversed, out-of-order inserts) | `TestReadsContract/entity_states_id_order` (fake + Postgres) |
| Porttest: MarkConsolidationFailed clears tagged scope z | `TestReadsContract/stale_scopes` (fake + Postgres) |

Gates: `make gen` leaves no diff. `make lint BREAKING_AGAINST=contracts-v1` reports 0 issues. `go test -race
-count=1 ./...` passes, including the Postgres contract suites.

## Decisions and CCRs
- **New decisions:**
  - 1600: the retain Reprocessor.
  - 1601: recall on the batch ports.
  - 1602: consolidate on the batch ports.
  - 1603: curate, docs and bank on the batch ports.
- **Updated decisions:** 802 (superseded), 808, 902, 1003, 1101, 1203 and 1300.
- **CCRs:** none.

## Constructor changes for WP-14b
- `consolidate.NewInvalidator()` takes no arguments. `Invalidator.ClearScopes` returns `([]port.UnitID, error)`.
- `ConsolidatorDeps` no longer has `Counter`, `Models` or `DocScopes`.
- `consolidate.NewStrategyMatcher(store.Read())`.
- `recall.RecallerDeps.Lookups` is gone. `recall.NewIncluder(r, count)`.
- `reflect.DefaultTools(rc, pages, store.Read())`.
- `retain.IngestorDeps.ScopesWriter` is gone. New: `retain.NewReprocessor(store)` is `docs.DocumentAdminDeps.Reprocessor`.
- `bank.NewTemplateCodec(store, refreshQueuer)`.
- `mentalmodel` Scope, Models, Cron and Failure sources are all `store.Read()`.
- `curate.ObservationAdminDeps{Scopes: store.Read(), Clearer: consolidate.NewInvalidator()}`.

## Known gaps
- **Reprocess loses some item fields.** Entity hints and `resolve_entities` are not stored anywhere, so a
  reprocess runs without them. A document with no live units reprocesses with no context and with
  mention time "now" (decision 1600).
- **Audit rows are not tagged.** Replaced units' audit and archive rows are not tagged with the reprocess
  operation id (SPEC 1970). No port method exists for it; this is carried from WP-08.
- **Stale threshold in the ChangeDetector.** The ChangeDetector resolves fuzzy scopes at recall's default
  trigram floor (0.45), because it has no config. The runner's pre-check uses the model's effective config.
- **Stale ignores the snapshot.** `Memory.Stale` reads the latest committed state, not the request's seq
  snapshot.
