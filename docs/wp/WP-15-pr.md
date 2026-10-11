# WP-15 — Port extension (CCR batch WP-15-1, plus CCR WP-13-2)

Branch `wp/WP-15-port-extension`. This lands as one change: the approved port and domain diff, migration
0010 and the schema dump, the Postgres adapter, the fake, the porttest suites and the regenerated mocks.
It also includes Blob.List and the gateway overflow classification.

## Built (card "Build")
- [x] Approved diff applied: `internal/port/{store,repos,queries,blob}.go` and `internal/domain/{unit,errors}.go`.
  CCR record: `docs/ccr/WP-15-1.md`.
- [x] Every new Reader method on Postgres `reader` (and `tx`, which embeds it), as sqlc queries:
  - `COLLATE "C"` for the text keysets.
  - Escaped folder prefix (`folder_path = $p OR LIKE $p || '/%'`).
  - StaleScopes, ListScopes and BanksToConsolidate share one rendering of the §0 predicate (`ScopeUnconsolidated` /
    the partial unconsolidated index).
  - NewestSeqInScope reuses `predicate.go`. This is dynamic SQL, the sanctioned exception.
  - TagCounts and BankStats use indexed COUNTs.
  - ListUnits is a UNION ALL over `memory_units` and `memory_units_archive`.
  - FailedRefreshes uses `DISTINCT ON (tenant_id, bank_id, serial_key)`.
- [x] `UnitRepo.IDsByDocument`, `EvidenceRepo.CitingScopes`, `DeleteScopes` and `DeleteAllObservations`.
  DeleteScopes and DeleteAllObservations share DeleteCiting's lock, delete and reset helper. Observations are
  locked `FOR UPDATE` in id order before their sources are read; DeleteCiting already did this and still does.
- [x] `SearchTemporal` honours `Embedding` and `MinSimilarity` in Postgres and the fake. The filter applies
  before bucketing and the limit.
- [x] The same methods on the fake's `reader`, `*tx`, `unitRepo` and `evidenceRepo`, with real semantics. The fake
  gained an `observation_scopes` table.
- [x] 1a = A: migration `0010_document_scopes.py`, `schema/schema.sql` (from `make migrate`). `DocumentRepo.Upsert` and
  `Get` (and ListDocuments) round-trip `Document.ObservationScopes` in both the adapter and the fake, and nil
  is stored as NULL.
- [x] Gateway `classify`: a non-retryable 400 or 413 whose body contains an overflow marker wraps
  `domain.ErrContextOverflow`.
- [x] Added in flight: CCR WP-13-2 `port.Blob.List` in the fake, the S3 adapter (ListObjectsV2 paging) and
  `RunBlobSuite`.
- [x] Added in flight: `Unconsolidated` and `BanksToConsolidate` skip units with no embedding (WP-13 review
  ruling).
- [x] Added in flight: FailedRefreshes considers only COMPLETED and FAILED operations (coordinator ruling on CCR
  WP-12-1).
- [x] Mocks regenerated.

## Acceptance tests

Each suite runs against the fake (`internal/fake/store_test.go`) and Postgres
(`internal/adapter/postgres/contract_test.go`). The tests are `TestReadsContract`, `TestCurationReadsContract`,
`TestMaintenanceContract` and `TestObservationAdminContract`. All pass on both.

| Accept item | Test |
|---|---|
| Tenant isolation, every new method | the per-method isolation checks inside each `RunReadsSuite` subtest; `RunCurationReadsSuite/tenant_isolation`; `RunMaintenanceSuite/cross_bank_tenants`; `RunObservationAdminSuite/tenant_isolation` |
| Error regimes | `RunReadsSuite/error_regimes`, `RunCurationReadsSuite/error_regimes`, `RunObservationAdminSuite/list_scopes_unknown_or_tombstoned` |
| GetChunks | `RunReadsSuite/get_chunks` |
| EntityStates | `RunReadsSuite/entity_states` |
| StaleScopes | `RunReadsSuite/stale_scopes` |
| TagCounts | `RunReadsSuite/tag_counts` |
| CountObservations | `RunReadsSuite/count_observations` |
| ListScopes | `RunObservationAdminSuite/list_scopes` |
| ListMentalModels | `RunCurationReadsSuite/list_mental_models` |
| NewestSeqInScope | `RunReadsSuite/newest_seq_in_scope` |
| CronModels | `RunMaintenanceSuite/cron_models` |
| FailedRefreshes | `RunMaintenanceSuite/failed_refreshes` |
| GetUnit and ListUnits | `RunCurationReadsSuite/get_unit`, `RunCurationReadsSuite/list_units` |
| ObservationHistory | `RunCurationReadsSuite/observation_history` |
| BankStats | `RunCurationReadsSuite/bank_stats` |
| ListDocuments | `RunCurationReadsSuite/list_documents` |
| ModelsCiting | `RunReadsSuite/models_citing` |
| BanksToConsolidate | `RunMaintenanceSuite/banks_to_consolidate` |
| Unembedded units are not consolidation candidates | `RunMaintenanceSuite/unconsolidated_skips_unembedded` |
| IDsByDocument | `RunObservationAdminSuite/ids_by_document` |
| CitingScopes | `RunObservationAdminSuite/citing_scopes` |
| DeleteScopes / DeleteAllObservations | `RunObservationAdminSuite/delete_scopes`, `RunObservationAdminSuite/delete_all_observations` |
| Temporal relevance filter | `RunReadsSuite/temporal_relevance_filter` |
| Document.ObservationScopes round-trip | `RunReadsSuite/document_observation_scopes` |
| Gateway overflow | `internal/adapter/gateway/overflow_test.go` `TestContextOverflowClassified` |
| Blob.List (CCR WP-13-2) | `RunBlobSuite/ListPaging`, `ListManyKeys` and `ListRejects` (fake and MinIO) |
| Final check | `make gen && make lint BREAKING_AGAINST=contracts-v1 && make test` pass; `make gen` leaves no diff |

## Decisions and CCRs
- `docs/ccr/WP-15-1.md` records the batch, the final rulings and the in-flight additions.
- `docs/decisions/1500-wp15-read-semantics.md` pins the semantics that the fake, Postgres and the tests share.
- `docs/decisions/1510-fake-wp15-reads.md` covers the fake: the observation_scopes table, Archive stamping seq,
  and the order of argument checks.
- `docs/decisions/1530-wp15-postgres-admin-reads-inputs.md` makes unknown enums and malformed cursors
  ErrInvalidArgument, and sorts NULL finished_at last.
- `docs/decisions/1540-reads-suite-test-readings.md` covers id-keyed lookups on a tombstoned bank that still has rows.
  It also records that byte order is shown with `"_x" < "a"` and `"z" < "é"`, because tags are lowercased.
- `docs/decisions/1560-listscopes-shared-scope-cursor.md`: `after == ""` lists from the start, including the
  shared scope, and `"0"` continues after a page that ends at the shared scope.

## Changes to existing behaviour
- `UnitRepo.Unconsolidated` no longer returns facts without an embedding. Five existing porttest cases now
  build their facts with `embeddedFact` (`internal/porttest/consolidation_fixture.go`).
- The fake's `UnitRepo.Archive` now takes `Tx.Seq` and stamps the archived unit, matching Postgres
  (decision 130).

## Engine follow-ups (beyond the CCR's §4)
- WP-13, and any caller paging ListScopes, must pass `after = "0"` after a page whose last scope is the shared
  scope (decision 1560).
- WP-11 `isContextOverflow` becomes `errors.Is(err, domain.ErrContextOverflow)`.
- WP-14 passes the blob store as `DeleterConfig.Blobs`.

## Known gaps
- Id-keyed lookups on a tombstoned bank. Accept §3 says these lookups return empty, but §0 says they never
  check the bank. Decision 1500 resolves the conflict in favour of §0: they return empty once delete_bank has
  emptied the bank, but may return rows while rows remain. The callers read the ids from a live bank first,
  which limits the exposure.
- Existing porttest files were edited. The card owns only new porttest files. The embedding ruling made
  five cases in `repo_suite.go` and `curation_suite.go` switch to `embeddedFact`; only their fixtures
  changed.
- The ListScopes `"0"` cursor rule (decision 1560) is now also in the `Reader.ListScopes` port doc, as a
  minimal doc-only fix to the §2 text.
- ListScopes runs three statements. On the pool reader, the counts can come from a later snapshot than the
  page.
- The ListUnits/ListDocuments tag CASE and the LIKE filters don't use the GIN or btree indexes. That is
  acceptable at v1 scale.
- PendingOperationsByKind is tested with pending jobs only, not processing ones.
- `"B" < "a"` can't be stored, because tags are normalized to lowercase. Byte order is tested with other
  pairs that flip under en_US collation.
