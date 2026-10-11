# Rules → tests

Every bullet and table row of SPEC "Jobs", "Curation" and "Behavioural rules", mapped to the tests that assert
it. Notation:

- `pkg.TestName` is a Go test in `internal/<pkg>` (or `cmd/memd`, `test/e2e`).
- `postgres.TestX/Case` is a case of one of the adapter's case tables (`TestStore`, `TestQueue`, `TestSeq`,
  `TestReads`, `TestCurationReads`, `TestMaintenanceReads`, `TestObservationAdmin`), each on its own Postgres
  database.
- `e2e.TestScenarios/X` runs against real Postgres + MinIO, the emulated gateway and memd as separate api and
  worker processes (`make e2e`).

Every name below was checked to exist (`func Test...` or a case string) when this file was written.

## Jobs

| Rule | Tests |
|---|---|
| A job row commits in the same transaction as its cause | `postgres.TestStore/TxCommitsEverything`, `postgres.TestStore/TxErrorRollsBackEverything`, `memory.TestRetainStampsSeqAndEnqueuesConsolidate` |
| retain_document: serial key `doc:{id}`, not deduplicated, enqueued by async Retain and ReprocessDocument | `memory.TestIngestAsyncEnqueuesChildrenUnderBatch`, `retain.TestReprocessCarriesScopesAndOverrides`, `e2e.TestScenarios/RetainAsync`, `e2e.TestScenarios/RetagReprocess` |
| retain_document: one tx per chunk batch, resumes by chunk hash, enqueues consolidate | `memory.TestRetainResumeAfterCrashReextractsOnlyRemainingChunks`, `memory.TestRetainChangedDocumentCrashResumeKeepsAllFacts`, `memory.TestRetainStampsSeqAndEnqueuesConsolidate` |
| retain_batch: parent only; its status aggregates its children | `postgres.TestSettleRetainBatches`, `postgres.TestSettleRetainBatchFailedChild`, `worker.TestRegisterEveryKindButRetainBatch`, `e2e.TestScenarios/RetainAsync` |
| consolidate: one per bank, pending dedupe unless it carries overrides or scopes | `postgres.TestQueue/DedupePending`, `postgres.TestQueue/DedupeOverridesNeverJoin`, `consolidate.TestRunScopedContinuationNeverJoins`, `worker.TestConsolidationReconcileJoinsRunning` |
| consolidate: up to 100 oldest units, every scope of each, re-enqueues while units remain (the handler never re-enqueues) | `consolidate.TestRunContinuationAtCap`, `consolidate.TestRunManualScopes`, `worker.TestConsolidateHandlerNeverReenqueuesOnMore`, `consolidate.TestRunClearedEarlierScopeIsNotStamped`, `consolidate.TestRunCoSourceInvalidatedIsNotStamped`, `consolidate.TestRunCoSourceRetaggedIsNotStamped` (a unit is stamped only while every scope this run wrote still cites it, decision 1801) |
| consolidate: triggers refreshes of stale models | `consolidate.TestRunTriggersRefreshes`, `consolidate.TestRunRefreshSurvivesFailedRun`, `mentalmodel.TestConsolidationTriggeredRefreshRunsAndStaysUnpaused`, `mentalmodel.TestRunJobAcceptsLegacyModelIDKey`, `e2e.TestScenarios/ConsolidationTriggersRefresh` (one payload shape, decision 1800) |
| rederive_memory: serial key of the unit's document; re-embed, re-resolve, rebuild links, enqueue consolidate | `memory.TestEditClearsEmbeddingAndQueuesRederive`, `memory.TestRederiveSkipsWhenUnitGone`, `memory.TestRederiveSkipsWhenTextChanged`, `e2e.TestScenarios/InvalidateRestore` |
| retag_document: pending dedupe; rewrite unit tags, clear observations, enqueue consolidate | `docs.TestRetagChangeClearsObservations`, `docs.TestRetagJobReadsCurrentTags`, `docs.TestRetagKeepsLabelTags`, `e2e.TestScenarios/RetagReprocess` |
| refresh_mental_model: serial key `mm:{id}`, a new request upgrades the queued one; agentic or single-shot | `mentalmodel.TestJoiningQueuedRefreshWithForceFullUpgrades`, `postgres.TestQueue/DedupeRefreshJoinsDespiteOverrides`, `mentalmodel.TestBothStrategiesProduceVersionThroughRunner`, `e2e.TestScenarios/MentalModelBothStrategies` |
| graph_maintenance: one per bank, pending-or-running dedupe, 240 s budget, re-enqueues if work is left | `maintenance.TestGraphMaintenanceResumesAfterBudget`, `memory.TestRemovalPathsQueueGraphMaintenance`, `worker.TestGraphHandlerReenqueuesAndRechecks` |
| delete_bank: batched deletes, blob prefix, then the bank row | `bank.TestBankDeleteLifecycle`, `bank.TestDeletePagesBlobsWithFencing`, `worker.TestDeleteBankReenqueuesUntilDone`, `e2e.TestScenarios/BankDelete` |
| The claim: SKIP LOCKED, serial key, unique partial index on processing rows | `postgres.TestQueue/EnqueueClaimRoundTrip`, `postgres.TestQueue/ClaimKindsLimitOldestFirst`, `postgres.TestQueue/ConcurrentClaims`, `postgres.TestAcceptSerialKeys`, `postgres.TestLostClaimRace` |
| One at a time per key | `postgres.TestQueue/SerialKeyOneAtATime`, `postgres.TestQueue/SerialKeysIndependent`, `postgres.TestQueue/ParkedOlderJobDoesNotBlock` |
| No model calls inside a transaction | `memd.TestWiredStackNoModelCallInsideTransaction` (every engine as wired, through retain, consolidation, recall, reflect, both refresh strategies and curation) |
| Heartbeats every 15 s; stale PROCESSING rows go back to PENDING or fail past max attempts | `postgres.TestQueue/HeartbeatPreventsReclaim`, `postgres.TestQueue/ReclaimStale`, `postgres.TestQueue/ReclaimPastMaxFails`, `worker.TestKilledWorkerJobReclaimed`, `worker.TestRunnerFencesItselfWithoutHeartbeats`, `e2e.TestScenarios/WorkerCrashReclaim` |
| Retries: backoff 60 s × attempt, 3 attempts; consolidate (and delete_bank) uncapped, skipping when another consolidate is pending; deterministic errors fail at once | `worker.TestRetryPolicy`, `worker.TestRunnerRetrySchedule`, `worker.TestRunnerConsolidateSkipsRetryWhenPending`, `worker.TestDeleteBankRetriesWithoutAttemptCap` |
| No dead-letter queue: FAILED stays, RetryOperation re-queues | `postgres.TestQueue/RetryFailedAndCancelled`, `e2e.TestScenarios/CancelRetry` |
| Bounded runs re-enqueue themselves | `consolidate.TestRunContinuationAtCap`, `maintenance.TestGraphMaintenanceResumesAfterBudget`, `worker.TestDeleteBankReenqueuesUntilDone`, `worker.TestGraphHandlerReenqueuesAndRechecks` |
| Slots: 10 per worker, 2 reserved for consolidate | `worker.TestRunnerReservedSlotsHoldUnderRetainFlood` |
| Maintenance tick: reclaim, cron refreshes, consolidation reconcile, prune; no leader, idempotent | `worker.TestMaintenanceStepOrderAndIsolation`, `worker.TestConcurrentMaintenanceNoDuplicateEnqueues`, `worker.TestPruneOperations`, `mentalmodel.TestCronRefreshes`, `mentalmodel.TestCronFiresOnScheduleWithinGrace`, `worker.TestMaintenanceRederivesStrandedFacts`, `postgres.TestStrandedFacts` |
| Debounce and parking; Reschedule releases a parked job | `mentalmodel.TestRefreshQueuer`, `mentalmodel.TestDecide`, `postgres.TestQueue/DedupePendingJoinsParked`, `postgres.TestQueue/RescheduleReleaseAndPark` |
| Events: written in the change's tx, numbered by the change seq, polled by after_seq; ListEvents limit 0 = 100, capped at 1000 | `postgres.TestStore/EventsAppendList`, `postgres.TestStore/EventsListNeverSplitsSeqGroup`, `postgres.TestStore/EventsTypeFilterAdvancesLastSeq`, `postgres.TestQueue/FailTerminalAppendsEvent`, `memd.TestListEventsDefaultAndCap` |

## Curation

| Rule | Tests |
|---|---|
| Edit: new values, edited_at, audit row; embedding cleared; citing observations deleted, co-sources reset; refreshes of models citing a deleted observation; rederive enqueued | `memory.TestEditClearsEmbeddingAndQueuesRederive`, `memory.TestCurationRefreshesModelsCitingDeletedObservations/edit`, `memory.TestEditClearEntities`, `memory.TestEditDateClearsTimeless`, `memory.TestEditKeepsUnitOutOfConsolidationUntilRederive`, `e2e.TestScenarios/InvalidateRestore` (edit step) |
| Edit job: re-embed, entities (literal or resolved), relink, enqueue consolidation | `memory.TestRederiveBackoffRetryKeepsNewerEntities`, `memory.TestRederiveCancelledMidTx`, `e2e.TestScenarios/InvalidateRestore` |
| Invalidate: archive with snapshots; delete links and entity rows; delete citing observations; graph_dirty; enqueue consolidate, graph_maintenance and refreshes of models citing the unit or a deleted observation | `memory.TestInvalidateRemovesCitingObservationsAndQueuesRefresh`, `memory.TestCurationRefreshesModelsCitingDeletedObservations/invalidate`, `memory.TestRemovalPathsQueueGraphMaintenance`, `postgres.TestStore/UnitsArchiveRestore`, `e2e.TestScenarios/InvalidateRestore` |
| Restore: move back, reattach entities and causal links; then as edit | `memory.TestRestoreRoundTrip`, `memory.TestRestoreAfterEntityPruneKeepsEntities`, `memory.TestRestoreLiveUnitIsNoop`, `e2e.TestScenarios/InvalidateRestore` |
| Recall and consolidation never see invalidated units | `postgres.TestStore/UnitsArchiveRestore`, `e2e.TestScenarios/InvalidateRestore` (recall excludes the fact) |
| Every curation calls tx.Seq | `memory.TestRestoreRoundTrip` (seq bumped), `e2e.TestScenarios/InvalidateRestore` (change_seq rises on invalidate, restore and edit) |
| ListMemories reads both tables; state selects one; entity_id matches live units only | `postgres.TestCurationReads/list_units`, `memory.TestListMemoriesPaging` |
| Reprocessing a document resets curation of its units | `retain.TestReprocessIgnoresCuratedUnit`, `docs.TestDocumentAdminGetAndReprocess`, `e2e.TestScenarios/RetagReprocess` |
| Audit and archive rows of replaced units are kept and tagged with the reprocess operation id | Not tested: the tagging is a known gap (no port method) |
| Observation history from observation_versions on every create and update; ListObservationHistory pages it newest first | `consolidate.TestRunCreateAndUpdateWriteVersionsAndEvidence`, `postgres.TestCurationReads/observation_history`, `observation.TestListObservationHistoryNewestFirst` |
| ClearObservations clears exactly one target (scopes, memory_id or all); no target is INVALID_ARGUMENT and deletes nothing | `observation.TestClearObservationsNeedsOneTarget`, `memd.TestClearObservationsNeedsTarget`, `observation.TestClearObservationsScopesAndAll`, `observation.TestClearObservationsMemory` |

## Behavioural rules

### Banks and documents

| Rule | Tests |
|---|---|
| A write to an unknown bank creates it; a read of an unknown bank is NOT_FOUND | `recall.TestRecallUnknownBank`, `e2e.TestScenarios/ConsolidationRecallReflect` (recall of an unknown bank), `e2e.TestScenarios/RetainSync` |
| DeleteBank: tombstone, cancel pending, enqueue delete_bank in one tx; writes FAILED_PRECONDITION, reads NOT_FOUND, claims skip, running jobs stop; batched delete, blob prefix, bank row; a later write creates a new bank | `bank.TestBankDeleteLifecycle`, `bank.TestTombstoneIdempotent`, `bank.TestDeleteRunStopsWhenCancelled`, `bank.TestRedeleteResumesHalfDeletedBank`, `postgres.TestQueue/CancelBank`, `postgres.TestQueue/DeletingBankClaims`, `postgres.TestQueue/BankDeleteRemovesOperationsAndEvents`, `e2e.TestScenarios/BankDelete` |
| Retain idempotency: same content + overrides hash is a no-op unless force_reextract | `memory.TestIngestUnchangedMakesNoLLMCall`, `memory.TestRetainCompletedJobReplayIsNoop`, `memory.TestRetainForceReextractCrashResume`, `memory.TestRetainSyncVictimPartialReplaceIsNotUnchanged`, `e2e.TestScenarios/RetainSync` |
| A changed document replaces old units in the first batch tx, deleting observations and resetting co-sources; an interrupted retain resumes | `memory.TestRetainReplacementDeletesObservationsAndResetsCoSources`, `memory.TestRetainResumeAfterCrashReextractsOnlyRemainingChunks`, `memory.TestRetainPartialReplaceAfterOtherRunCompletedIsNotUnchanged` |
| ALL_COMBINATIONS fan-out over max_combinations is INVALID_ARGUMENT synchronously | `memory.TestIngestFanOutRejectionStoresNothing`, `memory.TestIngestFanOutRejectionUnknownBankCreatesNothing`, `consolidate.TestScopeExpanderTooManyCombinations` |
| RetagDocument replaces the tag set; set-equal is a no-op; a change clears observations and re-consolidates | `docs.TestRetagSetEqualIsNoop`, `docs.TestRetagChangeClearsObservations`, `e2e.TestScenarios/RetagReprocess` |
| graph_maintenance refills links of graph_dirty neighbours below their caps and prunes orphan entities, in committed batches, resuming; an entity pruned between resolve and Attach is a conflict and the tx re-resolves | `maintenance.TestGraphMaintenanceTopsUpAndPrunes`, `maintenance.TestGraphMaintenanceResumesAfterBudget`, `maintenance.TestGraphMaintenanceRechecksAfterFinalBatch`, `postgres.TestStore/EntitiesAttachPrunedIsConflict`, `postgres.TestAttachAfterConcurrentPruneReresolves` |
| Config writes (UpdateBankConfig, ResetBankConfig, UpdateTenantConfig) serialize their read-modify-write, so concurrent patches of different fields both land | `postgres.TestConcurrentBankConfigPatchesKeepBoth`, `postgres.TestConcurrentTenantConfigPatchesKeepBoth`, `postgres.TestStore/BanksTenantConfigLockSerializes` |
| Change sequence: one bump per tx under the bank row lock; seq order is commit order | `postgres.TestSeq/SameValueWithinTx`, `postgres.TestSeq/StrictlyIncreasingAcrossTx`, `postgres.TestSeq/ConcurrentWritersSerialize`, `postgres.TestAcceptSeqOrder` |
| Stats and tag counts from COUNT queries; ListMemories returns no total | `bank.TestTagsPaging`, `postgres.TestCurationReads/bank_stats`, `postgres.TestReads/tag_counts` |

### Reflect

| Rule | Tests |
|---|---|
| Strict: a retrieval tool error fails with UNAVAILABLE; an empty retrieval is not a failure | `reflect.TestReflectRetrievalErrorIsUnavailable`, `reflect.TestReflectEmptyRetrievalIsNotFailure` |
| An empty final answer, or a model that can't drive tool calls, fails; a context overflow synthesises from what it has | `reflect.TestReflectEmptyAnswerFails`, `reflect.TestReflectModelWithoutToolCallsFails`, `reflect.TestReflectContextOverflowSynthesizes` |
| Past max_total_tokens fails with RESOURCE_EXHAUSTED | `reflect.TestReflectMaxTotalTokensExhausted`, `server.TestToStatus` |
| A malformed tool call goes back to the model | `reflect.TestReflectMalformedToolCallReturnsToModel` |

### Mental models

| Rule | Tests |
|---|---|
| refresh_after_consolidation and refresh_cron are mutually exclusive | `mentalmodel.TestCreateMentalModelValidatesAndQueuesBuild` |
| Two strategies, both honouring full/delta, scope filters and failure rules; set per bank, model or call | `mentalmodel.TestBothStrategiesProduceVersionThroughRunner`, `mentalmodel.TestDeltaAllOpsRejectedKeepsDocument`, `e2e.TestScenarios/MentalModelBothStrategies`, `memd.TestWiredStackNoModelCallInsideTransaction` |
| Staleness: in-scope seq above last_seq_seen, or a retracted source; untagged writes never stale a tagged model | `mentalmodel.TestUntaggedWriteNeverStalesTaggedModel`, `mentalmodel.TestRetractedSourceTriggersRefresh`, `mentalmodel.TestAdminGetDetailAndStale` |
| Watermark: read BankSeq first; a late writer is picked up by the next refresh | `mentalmodel.TestLateCommittingWriterPickedUpByNextRefresh` |
| Delta reads only what changed; source_ids accumulate across deltas and are pruned on full | `mentalmodel.TestDeltaRetrySucceedsAndSourcesAccumulate`, `mentalmodel.TestFullWithNothingDraftedKeepsLiveSources`, `mentalmodel.TestDeltaPreCheckReadsUntaggedUnits` |
| A delta never replaces the document with a partial one | `mentalmodel.TestDeltaAllOpsRejectedKeepsDocument`, `mentalmodel.TestDeltaEmptyRetryAfterAllRejectedFails`, `mentalmodel.TestApplyEditsAllRejectedAndEmpty` |
| Nothing in scope: no LLM call, only last_refreshed_at advances | `mentalmodel.TestNothingInScopeMeansNoLLMCall`, `mentalmodel.TestFullWithNothingInScopeKeepsSourcesAndWatermark` |
| Paused by failure until a manual refresh succeeds; failures in history | `mentalmodel.TestPauseAndManualUnpause`, `mentalmodel.TestReconcileSkipsFailureOfEarlierModel`, `mentalmodel.TestReconcileInterleavedWithWrite` |
| min_refresh_interval parks a triggered refresh; manual ones ignore the floor and release it | `mentalmodel.TestDecide`, `mentalmodel.TestAutomaticRunSkipsFreshOrFlooredModel`, `mentalmodel.TestFloorSkewTolerance` |
| At most one queued refresh per model; a new request joins and upgrades it; refreshes never overlap | `mentalmodel.TestJoiningQueuedRefreshWithForceFullUpgrades`, `mentalmodel.TestWeakerJoinerKeepsUpgrade`, `postgres.TestQueue/UpgradeMergesPayloadAndOverrides`, `postgres.TestQueue/SerialKeyOneAtATime` |
| DryRunRefresh runs the pipeline and writes nothing | `mentalmodel.TestDryRunWritesNothing` |

### Operations

| Rule | Tests |
|---|---|
| Cancel works on PENDING and PROCESSING; handlers stop at checkpoints; workers never overwrite CANCELLED | `postgres.TestQueue/CancelPending`, `postgres.TestQueue/CancelProcessingReportedLost`, `postgres.TestQueue/CancelledPendingFenced`, `worker.TestRunnerCancelledJobStopsAtCheckpoint`, `e2e.TestScenarios/CancelRetry` |
| Retry works on FAILED and CANCELLED, resetting to PENDING with run_at = now | `postgres.TestQueue/RetryFailedAndCancelled`, `e2e.TestScenarios/CancelRetry` |
| Progress at checkpoints; the runner heartbeats | `worker.TestCheckpointReport`, `postgres.TestQueue/Progress`, `postgres.TestQueue/HeartbeatPreventsReclaim` |
| Deterministic failures skip retries; transient ones back off | `worker.TestRetryPolicy`, `worker.TestRunnerRetrySchedule` |
| Large retains create a retain_batch parent aggregating its children; eval barrier = sync Retain + empty pending_operations_by_kind | `memory.TestIngestAsyncEnqueuesChildrenUnderBatch`, `postgres.TestSettleRetainBatches`, `e2e.TestScenarios/RetainAsync`, `e2e.TestGoldenAgainstMemdAll` |
| Terminal operations are pruned after the retention; pending and processing never are | `worker.TestPruneOperations`, `postgres.TestQueue/Prune`, `postgres.TestStore/EventsPrune` |

## Operation control and maintenance

| Rule | Tests |
|---|---|
| Cancel works on PENDING and PROCESSING operations, except delete_bank (FAILED_PRECONDITION, decision 2001) | `worker.TestControlDeleteBankAndDeletingBank` |
| Retry is refused with FAILED_PRECONDITION on a deleting bank, except for delete_bank | `worker.TestControlDeleteBankAndDeletingBank` |
| Maintenance tick: consolidation reconcile skips banks with auto_consolidation or enable_observations off, and only units older than the grace (decision 2000) | `worker.TestConsolidationReconcileSkipsDisabledBanks`, `worker.TestConsolidationReconcileGrace`, `postgres_test.TestUnconsolidatedBanks` |
| Maintenance tick: re-enqueues delete_bank for a tombstoned bank with no active delete | `worker.TestMaintenanceResumesStalledBankDelete`, `postgres_test.TestStalledBankDeletes` |
| Maintenance tick: enqueues graph_maintenance for banks with graph work | `worker.TestMaintenanceEnqueuesGraphMaintenance`, `postgres_test.TestBanksWithGraphWork` |
| Handlers write progress at checkpoints | `worker_test.TestHandlersReportProgress` |
| Bank delete removes the bank's blob prefix; writes during the delete fail with FAILED_PRECONDITION | `e2e.TestScenarios/BankDelete` |
