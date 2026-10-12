# Rules → tests

The behavioural rules of the design documents that decide correctness, each mapped to the tests that assert it.
A change to a rule changes its design section and its tests together. Notation:

- `pkg.TestName` is a Go test in the package of that name (`memd` = `cmd/memd`, `e2e` = `test/e2e`).
- `postgres.TestX/Case` is a case of one of the Postgres adapter's case tables (`TestStore`, `TestQueue`,
  `TestSeq`, `TestReads`, `TestCurationReads`, `TestMaintenanceReads`, `TestObservationAdmin`), each on its own
  database.
- `e2e.TestScenarios/X` runs against real Postgres and MinIO, the gateway emulator and memd as separate api and
  worker processes (`make e2e`).

Every name below exists in the code; re-check them after renaming a test.

## Jobs

Design: [architecture.md](architecture.md#job-queue).

| Rule | Tests |
|---|---|
| A job row commits in the same transaction as its cause | `postgres.TestStore/TxCommitsEverything`, `postgres.TestStore/TxErrorRollsBackEverything`, `memory.TestRetainStampsSeqAndEnqueuesConsolidate` |
| Every enqueue goes through the catalogue, which pins each kind's payload, serial key and dedupe policy | `jobs.TestCatalogueRows` |
| retain_document: serial key `doc:{id}`, not deduplicated, enqueued by async Retain and ReprocessDocument | `memory.TestIngestAsyncEnqueuesChildrenUnderBatch`, `document.TestReprocessDocument`, `e2e.TestScenarios/RetainAsync`, `e2e.TestScenarios/RetagReprocess` |
| retain_document: one transaction per chunk batch, resumes by chunk hash, enqueues consolidate | `memory.TestRetainResumeAfterCrashReextractsOnlyRemainingChunks`, `memory.TestRetainChangedDocumentCrashResumeKeepsAllFacts`, `memory.TestRetainStampsSeqAndEnqueuesConsolidate` |
| retain_batch: parent only; the tick settles it from its children | `postgres.TestSettleRetainBatches`, `postgres.TestSettleRetainBatchFailedChild`, `e2e.TestScenarios/RetainAsync` |
| consolidate: one per bank, pending dedupe unless it carries overrides or scopes | `postgres.TestQueue/DedupePending`, `postgres.TestQueue/DedupeOverridesNeverJoin`, `observation.TestRunScopedContinuationNeverJoins`, `maintenance.TestConsolidationReconcileJoinsRunning` |
| consolidate: up to `max_units_per_run` oldest facts, every scope of each; the run's last transaction enqueues the continuation | `observation.TestRunContinuationAtCap`, `observation.TestRunManualScopes`, `observation.TestRunConsolidateJobResult` |
| consolidate: a fact is stamped only while every scope that cites it still does ([consolidation.md](consolidation.md#stamping)) | `observation.TestRunClearedEarlierScopeIsNotStamped`, `observation.TestRunCoSourceInvalidatedIsNotStamped`, `observation.TestRunCoSourceRetaggedIsNotStamped`, `observation.TestRunRecheckCitedScopesBeforeStamp` |
| consolidate: triggers refreshes of the models its scopes reach, with one payload shape | `observation.TestRunTriggersRefreshes`, `observation.TestRunRefreshSurvivesFailedRun`, `mentalmodel.TestAutomaticRefreshRunsAndStaysUnpaused`, `e2e.TestScenarios/ConsolidationTriggersRefresh` |
| rederive_memory: serial key of the memory's document; re-embed, rebuild links, enqueue consolidate | `memory.TestEditClearsEmbeddingAndQueuesRederive`, `memory.TestRederiveSkipsWhenUnitGone`, `memory.TestRederiveSkipsWhenTextChanged`, `e2e.TestScenarios/InvalidateRestore` |
| retag_document: pending dedupe; clear observations, enqueue consolidate | `document.TestRetagChangeClearsObservations`, `document.TestRetagJobReadsCurrentTags`, `document.TestRetagKeepsLabelTags`, `e2e.TestScenarios/RetagReprocess` |
| refresh_mental_model: serial key `mm:{id}`, a new request upgrades the queued one; agentic or single-shot | `mentalmodel.TestJoiningQueuedRefreshWithForceFullUpgrades`, `postgres.TestQueue/DedupeRefreshJoinsDespiteOverrides`, `mentalmodel.TestBothStrategiesProduceVersionThroughRunner`, `e2e.TestScenarios/MentalModelBothStrategies` |
| graph_maintenance: one per bank, pending-or-running dedupe, 240 s budget, re-enqueues if work is left | `maintenance.TestGraphMaintenanceResumesAfterBudget`, `memory.TestRemovalPathsQueueGraphMaintenance`, `maintenance.TestGraphJobReenqueuesAndRechecks` |
| delete_bank: batched deletes, blob prefix, then the bank row | `bank.TestBankDeleteLifecycle`, `bank.TestDeletePagesBlobsWithFencing`, `bank.TestRunDeleteJobReenqueuesUntilDone`, `e2e.TestScenarios/BankDelete` |
| The claim: SKIP LOCKED, serial key, unique partial index on processing rows | `postgres.TestQueue/EnqueueClaimRoundTrip`, `postgres.TestQueue/ClaimKindsLimitOldestFirst`, `postgres.TestQueue/ConcurrentClaims`, `postgres.TestAcceptSerialKeys`, `postgres.TestLostClaimRace` |
| One job at a time per serial key; a parked older job does not block | `postgres.TestQueue/SerialKeyOneAtATime`, `postgres.TestQueue/SerialKeysIndependent`, `postgres.TestQueue/ParkedOlderJobDoesNotBlock` |
| No model call inside a transaction | `memd.TestWiredStackNoModelCallInsideTransaction` (every service as wired, through retain, consolidation, recall, reflect, both refresh strategies and curation) |
| Heartbeats; stale PROCESSING rows go back to PENDING or fail past the attempt cap; a run fences itself | `postgres.TestQueue/HeartbeatPreventsReclaim`, `postgres.TestQueue/ReclaimStale`, `postgres.TestQueue/ReclaimPastMaxFails`, `jobs.TestKilledWorkerJobReclaimed`, `jobs.TestRunnerFencesItselfWithoutHeartbeats`, `e2e.TestScenarios/WorkerCrashReclaim` |
| Retries: 60 s × attempt, 3 attempts; consolidate uncapped and skips its retry when another automatic one is due; delete_bank continues past the cap; permanent errors fail at once | `jobs.TestRetryPolicy`, `jobs.TestRunnerRetrySchedule`, `jobs.TestRunnerConsolidateSkipsRetryWhenPending`, `jobs.TestManualConsolidateRetriedDespitePending`, `bank.TestRunDeleteJobRetriesWithoutAttemptCap` |
| No dead-letter queue: FAILED stays, RetryOperation re-queues | `postgres.TestQueue/RetryFailedAndCancelled`, `e2e.TestScenarios/CancelRetry` |
| Bounded runs re-enqueue themselves | `observation.TestRunContinuationAtCap`, `maintenance.TestGraphMaintenanceResumesAfterBudget`, `bank.TestRunDeleteJobReenqueuesUntilDone`, `maintenance.TestGraphJobReenqueuesAndRechecks` |
| Slots: 10 per worker, 2 reserved for consolidate | `jobs.TestRunnerReservedSlotsHoldUnderRetainFlood` |
| Shutdown lets running jobs finish within the grace, then releases the rest | `jobs.TestRunnerShutdownGraceLetsJobsFinish`, `jobs.TestRunnerShutdownReleasesJobs` |
| Maintenance tick: ordered steps, no leader, idempotent, a failing step doesn't stop the rest | `maintenance.TestMaintenanceStepOrderAndIsolation`, `maintenance.TestConcurrentMaintenanceNoDuplicateEnqueues`, `maintenance.TestPruneOperations`, `mentalmodel.TestCronFiresOnScheduleWithinGrace`, `memory.TestRederiveStranded`, `postgres.TestStrandedFacts` |
| Debounce and parking; Reschedule releases a parked job | `mentalmodel.TestQueueModelRefresh`, `mentalmodel.TestDecide`, `postgres.TestQueue/DedupePendingJoinsParked`, `postgres.TestQueue/RescheduleReleaseAndPark` |
| Events: written in the change's transaction, numbered by the change seq, polled by after_seq; page_size 0 = 100, capped at 1000 | `postgres.TestStore/EventsAppendList`, `postgres.TestStore/EventsListNeverSplitsSeqGroup`, `postgres.TestStore/EventsTypeFilterAdvancesLastSeq`, `postgres.TestQueue/FailTerminalAppendsEvent`, `operation.TestListEventsPageBounds` |

## Curation

Design: [curation-and-operations.md](curation-and-operations.md#curation).

| Rule | Tests |
|---|---|
| Edit: new values, edited_at, audit row; embedding cleared; citing observations deleted, co-sources reset; refreshes of models citing a deleted observation; rederive enqueued | `memory.TestEditClearsEmbeddingAndQueuesRederive`, `memory.TestCurationRefreshesModelsCitingDeletedObservations/edit`, `memory.TestEditClearEntities`, `memory.TestEditDateClearsTimeless`, `memory.TestEditKeepsUnitOutOfConsolidationUntilRederive`, `e2e.TestScenarios/InvalidateRestore` |
| UpdateMemory applies only the masked fields | `grpcapi.TestUpdateMemoryMask` |
| Entities are resolved in the RPC's transaction; an older rederive never re-applies stale names | `memory.TestRederiveBackoffRetryKeepsNewerEntities`, `memory.TestRederiveCancelledMidTx`, `e2e.TestScenarios/InvalidateRestore` |
| Invalidate: archive with snapshots; delete citing observations; enqueue consolidate, graph_maintenance and refreshes of models citing the memory or a deleted observation | `memory.TestInvalidateRemovesCitingObservationsAndQueuesRefresh`, `memory.TestCurationRefreshesModelsCitingDeletedObservations/invalidate`, `memory.TestRemovalPathsQueueGraphMaintenance`, `postgres.TestStore/UnitsArchiveRestore`, `e2e.TestScenarios/InvalidateRestore` |
| Restore: move back, reattach entities and causal links, re-resolve pruned entities; restoring a live memory is a no-op | `memory.TestRestoreRoundTrip`, `memory.TestRestoreAfterEntityPruneKeepsEntities`, `memory.TestRestoreLiveUnitIsNoop`, `e2e.TestScenarios/InvalidateRestore` |
| Observations are never curated | `memory.TestCuratorRejectsObservations` |
| Recall and consolidation never see invalidated memories | `postgres.TestStore/UnitsArchiveRestore`, `e2e.TestScenarios/InvalidateRestore` |
| Every curation takes tx.Seq | `memory.TestRestoreRoundTrip`, `e2e.TestScenarios/InvalidateRestore` (change_seq rises on invalidate, restore and edit) |
| ListMemories reads live and archived memories; state selects one; entity_id matches live memories only | `postgres.TestCurationReads/list_units`, `memory.TestListMemoriesPaging` |
| Reprocessing a document resets the curation of its memories | `document.TestReprocessDocument`, `e2e.TestScenarios/RetagReprocess` |
| Observation history is written on every create and update and listed newest first | `observation.TestRunCreateAndUpdateWriteVersionsAndEvidence`, `postgres.TestCurationReads/observation_history`, `observation.TestListObservationHistoryNewestFirst` |
| ClearObservations clears exactly one target; none is INVALID_ARGUMENT and deletes nothing | `observation.TestClearObservationsNeedsOneTarget`, `memd.TestClearObservationsNeedsTarget`, `observation.TestClearObservationsScopesAndAll`, `observation.TestClearObservationsMemory`, `observation.TestClearMemoryLeavesPendingRederiveAlone` |

## Banks and documents

Design: [curation-and-operations.md](curation-and-operations.md#banks), [storage.md](storage.md#bank-deletion),
[retain.md](retain.md).

| Rule | Tests |
|---|---|
| A write to an unknown bank creates it; a read of an unknown bank is NOT_FOUND | `recall.TestRecallUnknownBank`, `config.TestEffectiveUnknownOrDeletingBank`, `e2e.TestScenarios/RetainSync` |
| A Retain item over ingest.max_document_bytes is INVALID_ARGUMENT naming items[k].content, and nothing is stored | `memory.TestIngestValidation` |
| CreateBank: an existing bank is ALREADY_EXISTS; a template creates directives and models and enqueues first builds, all or nothing | `bank.TestCreateBank`, `bank.TestTemplateRoundTrip`, `bank.TestTemplateCreateRollsBackOnBadSpec` |
| GetBank and every read of a deleting bank are NOT_FOUND; ListBanks leaves it out | `bank.TestGetBankDeleting`, `bank.TestListBanks` |
| DeleteBank: tombstone, cancel pending, enqueue delete_bank in one transaction; writes FAILED_PRECONDITION, reads NOT_FOUND, claims skip, running jobs stop; batched delete, blob prefix, bank row | `bank.TestBankDeleteLifecycle`, `bank.TestTombstoneIdempotent`, `bank.TestDeleteRunStopsWhenCancelled`, `bank.TestRedeleteResumesHalfDeletedBank`, `bank.TestDeleteFinalTxRechecksProcessingJobs`, `postgres.TestQueue/CancelBank`, `postgres.TestQueue/DeletingBankClaims`, `postgres.TestQueue/BankDeleteRemovesOperationsAndEvents`, `e2e.TestScenarios/BankDelete` |
| Cancel refuses delete_bank; retry on a deleting bank only for delete_bank | `operation.TestCancelAndRetryRules` |
| The tick re-enqueues delete_bank for a tombstoned bank with no active delete | `maintenance.TestMaintenanceResumesStalledBankDelete`, `postgres.TestStalledBankDeletes` |
| Retain idempotency: same content and no-op hash is a no-op unless force_reextract | `memory.TestIngestUnchangedMakesNoLLMCall`, `memory.TestRetainCompletedJobReplayIsNoop`, `memory.TestRetainForceReextractCrashResume`, `memory.TestRetainSyncVictimPartialReplaceIsNotUnchanged`, `e2e.TestScenarios/RetainSync` |
| A tombstoned bank refuses a retain before the blob is written | `memory.TestIngestDeletingBankStoresNoBlob` |
| The no-op and revision checks run again under the Seq lock: a retain completing between the pre-read and the write makes the item unchanged, and its extracted_at is never cleared (Retain and RetainBatch) | `memory.TestRetainNoopRecheckUnderSeq` |
| RetainBatch: a status per item, one bad item never fails the call; the highest revision of a document in the call wins (the last among equals), earlier items SUPERSEDED, a later lower revision STALE; a revision lower than the stored one is STALE and writes nothing | `memory.TestRetainBatchItemStatuses`, `e2e.TestScenarios/RetainBatch` |
| RetainBatch: a redelivered identical item joins the retain in flight, and once it completed is UNCHANGED with no model call, job or seq bump | `memory.TestRetainBatchRedeliveryIsIdempotent`, `e2e.TestScenarios/RetainBatch` |
| RetainBatch: over the bank's or org's pending retain backlog, the whole call is RESOURCE_EXHAUSTED (INGEST_BACKLOG) with a RetryInfo delay and writes nothing | `memory.TestRetainBatchBacklogWritesNothing`, `grpcapi.TestBacklogStatusAndItemError` |
| A changed document replaces old memories in the first batch transaction, deleting observations and resetting co-sources; a document being replaced stays in flight | `memory.TestRetainReplacementDeletesObservationsAndResetsCoSources`, `memory.TestRetainResumeAfterCrashReextractsOnlyRemainingChunks`, `memory.TestRetainPartialReplaceAfterOtherRunCompletedIsNotUnchanged` |
| Ownership: the latest retain wins; tags are adopted, not ownership | `memory.TestRetainSupersededAfterInterveningRetain`, `memory.TestRetainOverlappingRetainsOfOneDocument`, `memory.TestRetainRetaggedBetweenBatchesKeepsAllFacts`, `memory.TestRetainOlderRunCannotOverwriteNewerTags`, `memory.TestRetainOverridesChangeMidRunSupersedes` |
| ALL_COMBINATIONS fan-out over max_combinations is INVALID_ARGUMENT synchronously | `memory.TestIngestFanOutRejectionStoresNothing`, `memory.TestIngestFanOutRejectionUnknownBankCreatesNothing`, `domain.TestExpandScopesTooManyCombinations` |
| RetagDocument replaces the tag set; set-equal is a no-op; a change clears observations and re-consolidates | `document.TestRetagSetEqualIsNoop`, `document.TestRetagChangeClearsObservations`, `document.TestRetagSetEqualAfterFailedRetagReenqueues`, `memd.TestRetagDocumentTags`, `e2e.TestScenarios/RetagReprocess` |
| graph_maintenance tops up links of graph_dirty neighbours and prunes orphan entities in committed batches; an entity pruned between resolve and attach is a conflict and the transaction re-resolves | `maintenance.TestGraphMaintenanceTopsUpAndPrunes`, `maintenance.TestGraphMaintenanceResumesAfterBudget`, `maintenance.TestGraphMaintenanceRechecksAfterFinalBatch`, `maintenance.TestGraphMaintenanceStopsOnTombstone`, `postgres.TestStore/EntitiesAttachPrunedIsConflict`, `postgres.TestAttachAfterConcurrentPruneReresolves` |
| Config writes serialize their read-modify-write, so concurrent patches of different fields both land | `configsvc.TestConcurrentBankConfigPatchesKeepBoth`, `configsvc.TestConcurrentTenantConfigPatchesKeepBoth`, `postgres.TestStore/BanksTenantConfigLockSerializes` |
| Change sequence: one bump per transaction under the bank row lock; seq order is commit order | `postgres.TestSeq/SameValueWithinTx`, `postgres.TestSeq/StrictlyIncreasingAcrossTx`, `postgres.TestSeq/ConcurrentWritersSerialize`, `postgres.TestAcceptSeqOrder` |
| Orgs must exist: a write or bank config update under an unknown org is NOT_FOUND (ORG_NOT_FOUND) and creates nothing; a write to an unknown bank of an existing org creates the bank; CreateOrg of an existing org is ALREADY_EXISTS; DeleteOrg of an unknown org is NOT_FOUND | `memd.TestOrgsMustExist`, `postgres.TestOrgs`, `config.TestEffectiveUnknownOrDeletingBank`, `e2e.TestScenarios/ConsolidationRecallReflect`, `bank.TestListBanks` |
| Bank creation share-locks the org: it waits for an org delete holding the row FOR UPDATE and then fails ORG_DELETING; org config updates never block it | `postgres.TestEnsureWaitsForOrgDelete`, `postgres.TestOrgConfigUpdateDoesNotBlockBanks` |
| DeleteOrg: one transaction tombstones the org and its banks, cancels their pending jobs, starts the receipt and enqueues delete_org; writes are ORG_DELETING, reads NOT_FOUND (GetOrg included); re-issuing joins; delete_org can't be cancelled | `memd.TestDeleteOrgWhileRunning`, `postgres.TestOrgDeleteStatements`, `org.TestDeleteOrgResumesAfterCrash` |
| A bank created concurrently with DeleteOrg is tombstoned with the others or refused; none survives | `postgres.TestBankCreatedDuringOrgDelete`, `memd.TestRetainRacingDeleteOrg` |
| delete_org fans out to delete_bank, sweeps the org's rows, deletes its blobs after the grace, deletes the org and completes the receipt; nothing of the org is left and other orgs are untouched | `memd.TestDeleteOrgEndToEnd`, `e2e.TestScenarios/OrgDelete`, `postgres.TestOrgDeleteStatements` |
| A retain racing DeleteOrg leaves no row and no blob of the org after completion | `memd.TestRetainRacingDeleteOrg` |
| Org-scoped operations: claimed and alive under a tombstoned org (only delete_org), failed by reclaim without an event, retryable on the tombstoned org | `postgres.TestOrgScopedJobLifecycle` |
| A worker crash mid-delete resumes: the reclaim fails it at its cap, resume_org_deletes re-enqueues it, the next run finishes | `org.TestDeleteOrgResumesAfterCrash` |
| Verification counts every table with org_id from the schema; a leftover row leaves the receipt FAILED_VERIFICATION and the delete retrying, the org in place | `org.TestDeleteOrgVerificationCatchesLeftover`, `postgres.TestCountRowsSeesNewTables` |
| The org sweep covers every table with org_id but banks, operations and lifecycle_events | `postgres.TestOrgSweepCoversSchema` |
| Deletion receipts: found by org id hash (newest first), never another org's; only completed ones are pruned | `postgres.TestReceipts` |
| A bank shares nothing with the same bank id in another tenant or another org | `postgres.TestStore/TenantIsolation`, `postgres.TestStore/OrgIsolation`, `postgres.TestStore/OrgIsolationSetDocumentTags`, `postgres.TestStore/OrgIsolationDirtyClearDirty`, `postgres.TestStore/OrgIsolationEntityMaintenance`, `postgres.TestStore/OrgIsolationDirectives` |
| Every bank reference names its org: no keyed BankRef literal, the store refuses an empty org, every statement naming a bank names org_id, every bank-scoped table has org_id | `grpcapi.TestNoBankRefLiterals`, `postgres.TestRejectEmptyOrg`, `postgres.TestSQLNamesOrg`, `postgres.TestSchemaOrgColumns`, `grpcapi.TestBankRef` |
| Config levels merge server < tenant < org < bank; the org level has its own views, patch and reset | `config.TestEffectivePrecedence`, `memd.TestOrgConfigLevel` |
| ListOrgs pages the tenant's live orgs by id with id_prefix; another tenant's orgs never show | `memd.TestListOrgsPaging`, `postgres.TestOrgs` |
| Stats and tag counts come from COUNT queries; listings return no total | `bank.TestListTagsPaging`, `postgres.TestCurationReads/bank_stats`, `postgres.TestReads/tag_counts` |

## Consolidation

Design: [consolidation.md](consolidation.md).

| Rule | Tests |
|---|---|
| A poison fact is parked only after a successful health probe; an outage parks nothing | `observation.TestRunHalvesDownToPoisonUnit`, `observation.TestRunLonePoisonParked`, `observation.TestRunPoisonAloneInScopeDoesNotBlockOthers`, `observation.TestRunOutageDoesNotPark`, `observation.TestRunOutageMidRunDoesNotPark`, `observation.TestSettleProbesTheScopeBinding` |
| Two dead batches in a row fail the run | `observation.TestCircuitBreakerAfterTwoDeadBatches` |
| A replayed run is safe: scopes that already cite a fact are skipped | `observation.TestRunReplayAfterKillAtThirdScope` |
| Dedup merges a near-duplicate create into its twin, or keeps it | `observation.TestRunDedupMerge`, `observation.TestRunDedupKeep`, `observation.TestRunDedupBelowThresholdSkipsCheck` |
| The first matching strategy is a level between bank and request | `observation.TestRunStrategyOverridesBankNotRequest`, `config.TestForScopeStrategyBetweenBankAndRequest`, `config.TestMatchStrategy`, `observation.TestStrategyPreview` |
| The strategy preview pages the bank's scopes, and the pages add up to the whole bank | `observation.TestStrategyPreview`, `memd.TestListPaging` |
| The tick consolidates only banks with auto consolidation and observations on, after the reconcile grace | `observation.TestReconcileSkipsDisabledBanks`, `observation.TestReconcileGrace`, `postgres.TestUnconsolidatedBanks` |
| The tick enqueues graph_maintenance for banks with graph work | `maintenance.TestMaintenanceEnqueuesGraphMaintenance`, `postgres.TestBanksWithGraphWork` |

## Recall

Design: [recall.md](recall.md).

| Rule | Tests |
|---|---|
| Arms are selected by name; an unknown or repeated name is INVALID_ARGUMENT; an extra arm needs no engine change | `recall.TestRecallArmSelection`, `recall.TestRecallExtraArm`, `config.TestCheckArms`, `arms.TestContract` |
| Every hit is re-checked against the request's bank and filters | `recall.TestRecallExtraArm` |
| RRF fusion in arm order; boosts only decide the rerank cut | `recall.TestFuseRRFOrder`, `recall.TestFuseStrategyBoostRankSpace`, `recall.TestRecallFusionOrderGolden` |
| A failing arm fails the recall | `recall.TestRecallFailingArmFailsRecall` |
| An entity lists its 50 most recently updated observations and counts them all | `postgres.TestReads/entity_states_observation_cap`, `postgres.TestReads/entity_states` |
| A Recall's max_tokens (and include budgets) is capped at api.max_recall_tokens before packing | `memd.TestRecallTokenCap` |
| Packing skips an oversized result and returns the top one when nothing fits | `recall.TestPackSkipsOversized`, `recall.TestPackFloorWhenNothingFits` |
| No time cue means no TIME_PARSE call | `timex.TestNoCueMakesNoLLMCall`, `arms.TestRecallNoCueNoTimeParse`, `arms.TestRecallExplicitWindowNoTimeParse` |

## Reflect

Design: [reflect-and-mental-models.md](reflect-and-mental-models.md#reflect).

| Rule | Tests |
|---|---|
| A retrieval tool error fails with UNAVAILABLE; an empty retrieval is not a failure | `reflect.TestReflectRetrievalErrorIsUnavailable`, `reflect.TestReflectEmptyRetrievalIsNotFailure` |
| An empty final answer, or a model that can't drive tool calls, fails; a context overflow synthesizes from what it has | `reflect.TestReflectEmptyAnswerFails`, `reflect.TestReflectModelWithoutToolCallsFails`, `reflect.TestReflectContextOverflowSynthesizes` |
| Past max_total_tokens fails with RESOURCE_EXHAUSTED | `reflect.TestReflectMaxTotalTokensExhausted`, `grpcapi.TestToStatus` |
| A malformed tool call goes back to the model | `reflect.TestReflectMalformedToolCallReturnsToModel` |
| Citations are kept only when retrieved | `reflect.TestReflectCitationFiltering`, `memd.TestReflectBasedOn` |
| One directive-selection rule for reflect, the synthesize prompt and the preview | `domain.TestSelectDirectives`, `mentalmodel.TestSynthesizeDirectivesMatchReflect` |

## Mental models

Design: [reflect-and-mental-models.md](reflect-and-mental-models.md#mental-models).

| Rule | Tests |
|---|---|
| A model is created with its first build queued; a cron must parse | `mentalmodel.TestCreateMentalModelValidatesAndQueuesBuild`, `mentalmodel.TestCreateMentalModelRejectsInvalidSpecs`, `mentalmodel.TestParseCronStrict`, `grpcapi.TestFieldMaskScheduleOneof` |
| Two strategies, both honouring full and delta, scope filters and failure rules | `mentalmodel.TestBothStrategiesProduceVersionThroughRunner`, `mentalmodel.TestDeltaAllOpsRejectedKeepsDocument`, `e2e.TestScenarios/MentalModelBothStrategies`, `memd.TestWiredStackNoModelCallInsideTransaction` |
| Staleness: an in-scope seq above last_seq_seen, or a retracted source; untagged writes never stale a tagged model; fuzzy leaves use the bank's floor | `mentalmodel.TestUntaggedWriteNeverStalesTaggedModel`, `mentalmodel.TestAdminGetViewAndStale`, `mentalmodel.TestStalenessUsesBankFuzzyFloor`, `mentalmodel.TestFuzzyScopeResolvesBankTags` |
| Watermark: read BankSeq first; a late writer is picked up by the next refresh | `mentalmodel.TestLateCommittingWriterPickedUpByNextRefresh` |
| A delta reads only what changed; sources accumulate across deltas and are replaced on full; a requested delta falls back to full for a recorded reason | `mentalmodel.TestDeltaRetrySucceedsAndSourcesAccumulate`, `mentalmodel.TestFullWithNothingDraftedKeepsLiveSources`, `mentalmodel.TestDeltaPreCheckReadsUntaggedUnits`, `mentalmodel.TestModeFallbackReasons`, `mentalmodel.TestMaxDeltaRatioFallsBackToFull` |
| A delta never replaces the document with a partial one | `mentalmodel.TestDeltaAllOpsRejectedKeepsDocument`, `mentalmodel.TestDeltaEmptyRetryAfterAllRejectedFails`, `mentalmodel.TestApplyEditsAllRejectedAndEmpty` |
| Nothing in scope: no LLM call, only last_refreshed_at advances | `mentalmodel.TestNothingInScopeMeansNoLLMCall`, `mentalmodel.TestFullWithNothingInScopeKeepsSourcesAndWatermark` |
| A refresh whose model changed since it read it fails with a conflict | `mentalmodel.TestRefreshConflictWhenModelChanged`, `mentalmodel.TestScopeEditDuringFirstBuildConflicts` |
| Paused by failure until a manual refresh succeeds; failures go to history | `mentalmodel.TestPauseAndManualUnpause`, `mentalmodel.TestReconcileSkipsFailureOfEarlierModel`, `mentalmodel.TestReconcileInterleavedWithWrite` |
| The interval floor parks an automatic refresh; manual ones ignore it and release it | `mentalmodel.TestDecide`, `mentalmodel.TestAutomaticRunSkipsFreshOrFlooredModel`, `mentalmodel.TestFloorSkewTolerance` |
| At most one queued refresh per model; a new request joins and upgrades it; refreshes never overlap | `mentalmodel.TestJoiningQueuedRefreshWithForceFullUpgrades`, `mentalmodel.TestWeakerJoinerKeepsUpgrade`, `postgres.TestQueue/UpgradeMergesPayloadAndOverrides`, `postgres.TestQueue/SerialKeyOneAtATime` |
| PreviewMentalModelRefresh runs the pipeline and writes nothing | `mentalmodel.TestPreviewWritesNothing` |
| A page over 1 MiB fails the refresh for good (content_too_large) and keeps the stored page | `mentalmodel.TestOversizedPageKeepsDocument` |

## Operations and the API

Design: [curation-and-operations.md](curation-and-operations.md).

| Rule | Tests |
|---|---|
| Cancel works on PENDING and PROCESSING; handlers stop at checkpoints; workers never overwrite CANCELLED | `postgres.TestQueue/CancelPending`, `postgres.TestQueue/CancelProcessingReportedLost`, `postgres.TestQueue/CancelledPendingFenced`, `jobs.TestRunnerCancelledJobStopsAtCheckpoint`, `e2e.TestScenarios/CancelRetry` |
| Retry works on FAILED and CANCELLED, resetting to PENDING with run_at = now | `postgres.TestQueue/RetryFailedAndCancelled`, `e2e.TestScenarios/CancelRetry` |
| Progress at checkpoints; results through jobs.Run; the runner heartbeats | `jobs.TestRunReport`, `jobs.TestRunnerReportsResult`, `postgres.TestQueue/Progress`, `postgres.TestQueue/HeartbeatPreventsReclaim` |
| Async retains create a retain_batch parent; eval barrier = sync Retain plus an empty pending_operations_by_kind | `memory.TestIngestAsyncEnqueuesChildrenUnderBatch`, `postgres.TestSettleRetainBatches`, `e2e.TestScenarios/RetainAsync`, `e2e.TestGoldenAgainstMemdAll` |
| Terminal operations are pruned after the retention; pending and processing never are | `maintenance.TestPruneOperations`, `postgres.TestQueue/Prune`, `postgres.TestStore/EventsPrune` |
| Every RPC is wired | `memd.TestEveryRPCWired` |
| Errors carry ErrorInfo reasons and BadRequest field violations; a batch item's error carries the same code, reason and fields (under `items[k]`) | `grpcapi.TestToStatus`, `grpcapi.TestToStatusFieldViolations`, `grpcapi.TestBacklogStatusAndItemError` |
| Paging: a negative page_size or max_results is INVALID_ARGUMENT | `grpcapi.TestPagingInterceptor`, `operation.TestListEventsPageBounds` |
| Paging: every RPC with page_size serves a size over 1000, rejects a malformed token naming page_token, and a one-item walk lists exactly one default page, in order | `memd.TestListPaging` |
| A page of large items ends early under api.max_response_bytes, keeps at least one item and continues after the last one kept | `memory.TestListMemoriesResponseBudget` |
| A read whose response passes api.max_response_bytes fails with RESPONSE_TOO_LARGE; a write never does | `grpcapi.TestResponseSizeInterceptor` |
| FieldMask patches: implicit masks touch only populated leaves; reset needs a mask, `*` resets all | `grpcapi.TestFieldMaskImpliedLeaves`, `grpcapi.TestFieldMaskConfigPatch`, `grpcapi.TestResetMask`, `config.TestResetPaths`, `memd.TestConfigWritesValidated` |
| Model routing: per slot, the most specific level yielding a binding wins, its own or its default | `config.TestModelResolution`, `config.TestMergePrecedence` |
| Health needs no tenant; readiness runs the probes | `grpcapi.TestHealthWithoutTenant`, `grpcapi.TestServeHealth` |

## Ingest boundary

Design: [architecture.md](architecture.md#ingest-boundary). Every adapter runs `ingesttest.Run`: `memq`, the
in-memory adapter (`memq.TestContract`), and `kafka` on a real broker (`kafka.TestContract`, which skips the lease
case: Kafka has no lease).

| Rule | Tests |
|---|---|
| An unsettled message is redelivered after Close, and after the lease runs out | `memq.TestContract/UnsettledRedeliveredAfterClose`, `memq.TestContract/UnsettledRedeliveredAfterLease`, `kafka.TestContract/UnsettledRedeliveredAfterClose` |
| Ack settles for good, and is idempotent | `memq.TestContract/AckSettlesForGood`, `kafka.TestContract/AckSettlesForGood` |
| A stale settlement (after Close or a redelivery) neither fails nor settles the redelivered message | `memq.TestContract/StaleSettlementIsHarmless`, `kafka.TestContract/StaleSettlementIsHarmless` |
| Nack redelivers no sooner than its delay | `memq.TestContract/NackDelaysRedelivery`, `memq.TestNackKeepsKeyOrder`, `kafka.TestContract/NackDelaysRedelivery` |
| A dead-lettered message never returns on the main subscription | `memq.TestContract/DeadLetterNeverReturns`, `kafka.TestContract/DeadLetterNeverReturns` |
| Messages with one key arrive in publish order | `memq.TestContract/SameKeyKeepsPublishOrder`, `kafka.TestContract/SameKeyKeepsPublishOrder` |
| Receive honours max, returns empty when idle, the context's error when cancelled and ErrClosed after Close | `memq.TestContract/ReceiveIdle`, `memq.TestContract/ReceiveHonoursMax`, `memq.TestContract/ReceiveContextCancelled`, `memq.TestContract/ReceiveAfterClose`, `kafka.TestContract/ReceiveIdle`, `kafka.TestContract/ReceiveHonoursMax`, `kafka.TestContract/ReceiveContextCancelled`, `kafka.TestContract/ReceiveAfterClose` |
| An adapter without a dead-letter destination fails at construction | `kafka.TestNewRequiresDeadLetterTopic` |
| Kafka commits a partition only past contiguously settled records | `kafka.TestCommitOnlyPastContiguousAcks` |
| A Kafka dead letter keeps the key, value and headers and adds the reason and source position | `kafka.TestDeadLetterCarriesReason` |

## Queue ingestion

Design: [retain.md](retain.md#queue-ingestion).

| Rule | Tests |
|---|---|
| Settlement by outcome: ACCEPTED, UNCHANGED, STALE, SUPERSEDED Ack; REJECTED and permanent call errors DeadLetter; FAILED and transient errors Nack with doubling backoff, DeadLetter at max attempts; the error class wins over the status (ORG_DELETING drops, BANK_DELETING and ORG_NOT_FOUND without a receipt retry) | `ingestapi.TestDecideSettlement` |
| The backlog rejection Nacks after its RetryInfo delay, pauses receiving and never counts towards the attempts | `ingestapi.TestDecideSettlement/backlog_honours_RetryInfo_and_pauses` |
| Envelopes decode as protojson or binary protobuf; an undecodable body, a tenant other than the pinned one and an envelope the RPC would refuse are dead-lettered with their reason | `ingestapi.TestConsumerSettlesByEnvelope`, `e2e.TestScenarios/QueueIngestKafka` |
| A message for a deleted org (not live, with a deletion receipt) is acked and dropped, never retained or dead-lettered; one for an org not created yet is retried | `ingestapi.TestConsumerDropsDeletedOrg` |
| A message delivered twice (a lost Ack, a producer retry) yields one document, one retain_document and one extraction | `memd.TestIngestRedeliveryRetainsOnce`, `e2e.TestScenarios/QueueIngestKafka` |
| Shutdown stops receiving, finishes the in-flight RetainBatch, settles its messages, then closes the source | `ingestapi.TestConsumerShutdownSettlesInFlight` |
| memd ingest needs a provider, and kafka its brokers, topic, group and dead-letter topic; the consumer batch fits max_batch_items | `memd.TestValidateRejects` |
