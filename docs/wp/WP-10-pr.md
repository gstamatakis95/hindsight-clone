# WP-10 — Consolidation engine

Branch `wp/WP-10-consolidate`. Owns `internal/engine/consolidate/**`. The engine runs on the fakes; the
real ports need CCR WP-10-1 (now in the WP-15 batch) and WP-14 wiring.

## Built
- [x] `Consolidator.Run`. It takes up to `consolidation_max_units_per_run` oldest unconsolidated units; a
  manual run takes only units in its scopes. Each unit is decided in every scope it belongs to, and a
  scope that already cites the unit is skipped. The unit is stamped in the same tx as its last scope's
  writes. Accepts the empty `{}` payload, and `{"scopes":[{"tags":[...]}]}` (= WP-13's
  `curate.ConsolidatePayload`).
- [x] Decide outside any tx, apply inside one short tx per batch.
  - `decide` runs with no tx open: related observations, CONSOLIDATE, embeddings, dedup.
  - `apply` re-reads the facts and takes `tx.Seq`. It writes rows stamped with the seq, evidence and
    versions, and the refreshes of reached models. It then stamps. Facts changed since the plan are never
    quoted or stamped.
- [x] `observation_versions` row on every create and update, carrying all source ids. Evidence is unique
  per (observation, fact), and each quote is the fact's exact text.
- [x] Dedup: cosine >= `dedup_threshold` (default 0.97; >= 1 disables), then DEDUP_CHECK. Merge folds the
  create into the twin; keep creates it.
- [x] Per-scope observation cap: capacity note in the prompt, `creates.maxItems` in the schema, and a
  defensive truncation.
- [x] Per-call schema injection: the batch's ids become the `source_fact_ids` enum, with `minItems: 1`.
- [x] LLM batches of `consolidation_llm_batch_size`. A failing batch is halved. A unit that fails alone is
  deferred until its batch ends. Then a health probe runs on the scope's binding, the unit is retried
  alone, and it is parked only if it fails again. A failed probe, or two dead batches in a row, fails the
  run and parks nothing (decision 1002).
- [x] `More` triggers a continuation, enqueued by `Run` in its final tx with NewJob's dedupe rule.
- [x] Refresh triggers in every writing apply tx (debounced, interval floor) and `CONSOLIDATION_COMPLETED`
  in the final tx.
- [x] `ScopeExpander` (all five modes), `StrategyMatcher` (Match + Preview) and `Invalidator`.

## Acceptance tests (all pass, `go test -race`)
| Card item | Test |
|---|---|
| A unit in 3 scopes, killed after scope 2, then replayed: no duplicate observations, stamped once | `TestRunReplayAfterKillAtThirdScope` |
| Halving down to a single poison unit | `TestRunHalvesDownToPoisonUnit`, `TestRunPoisonLastLeafParkedByProbe`, `TestRunLonePoisonParked`, `TestRunPoisonAloneInScopeDoesNotBlockOthers`, `TestRunOutageDoesNotPark`, `TestRunOutageMidRunDoesNotPark` |
| Continuation at the cap | `TestRunContinuationAtCap`, `TestRunScopedContinuationNeverJoins` |
| A strategy overriding the bank but not the request | `TestRunStrategyOverridesBankNotRequest` |
| The dedup merge and keep paths | `TestRunDedupMerge`, `TestRunDedupKeep`, `TestRunDedupBelowThresholdSkipsCheck` |
| Every scope mode expands correctly | `TestScopeExpanderModes`, `TestScopeExpanderTooManyCombinations`, `TestScopeExpanderRejectsNonPositiveCap`, `TestCombinations` |

## Review round 1 fixes
| Item | Fix | Test |
|---|---|---|
| H1 poison isolation | A poison unit no longer blocks its scope or the bank. It is settled after its batch by a health probe (a no-fact CONSOLIDATE call), which parks it or fails the run. Round 2 refines how it parks (see below). | `TestRunPoisonAloneInScopeDoesNotBlockOthers`, `TestRunLonePoisonParked`, `TestRunPoisonLastLeafParkedByProbe`, `TestRunOutageDoesNotPark`, `TestRunOutageMidRunDoesNotPark` |
| H2 concurrent edit/retag | Facts whose seq or tags changed since the plan are not live: their evidence is dropped and they are not stamped (apply and finish) | `TestRunEditBetweenScopesIsNotStamped`, `TestRunRetagMidRunIsNotStamped` |
| H3 no resurrection | An action with a non-live fact or a gone target is skipped whole, never rewritten or trimmed. The batch stays unstamped | `TestRunGoneTargetIsNotResurrected`, `TestRunArchivedEvidenceSkipsAction` |
| H4 refresh triggers | Models are listed once per run; every writing apply tx enqueues refreshes | `TestRunRefreshSurvivesFailedRun`, `TestRunTriggersRefreshes` |
| M1 re-check skipped scopes | `Cites` is re-checked before stamping | `TestRunRecheckCitedScopesBeforeStamp` |
| M2 continuation dedupe | `NewJob(bank, scopes, overrides)`: DedupeNone with scopes or overrides | `TestRunScopedContinuationNeverJoins` |
| M3 double update | The first update of an observation wins and later ones are dropped (no evidence union). The batch stays unstamped | `TestFirstUpdatesKeepsFirst`, `TestRunDoubleUpdateLeavesBatchUnstamped` |
| M4 versions and quotes | A recording Store decorator asserts the version rows and the exact quotes | `TestRunVersionsAndQuotesRecorded` |
| M5 ClearScopes | Enqueues nothing (decision 1005) | `TestInvalidatorClearScopesAndDocument` |
| LOW | `NewInvalidator(nil)` returns FAILED_PRECONDITION; `max_combinations <= 0` is rejected; combinations doc fixed; any/all patterns documented to match the shared scope | `TestInvalidatorWithoutClearer`, `TestScopeExpanderRejectsNonPositiveCap` |
| Ruling (1003) | Per-item scopes are read from `Document.ObservationScopes` (WP-15) through `DocumentScopeReader`, falling back to overrides, then the bank default | `TestRunItemScopesBeatOverrides`, `TestRunDocumentScopeMode` |

## Review round 2 fixes
| Item | Fix | Test |
|---|---|---|
| HIGH apply Seq | apply takes `tx.Seq` first in every transaction, before the re-read (no lazy path), so a stamp-only batch also locks the bank row | `TestApplyTakesSeqFirst` |
| HIGH probe binding | settle probes with the failing scope's config and binding; deferred units never cross batches, so they share it | `TestSettleProbesTheScopeBinding` |
| HIGH probe isn't enough | after a successful probe each deferred unit is retried alone once; only units that fail again are parked | `TestSettleRetriesAloneBeforeParking` |
| MEDIUM park | park takes `tx.Seq`, re-reads the units and parks only those unchanged since the plan | `TestParkSkipsChangedUnits` |
| MEDIUM circuit breaker | two consecutive dead batches with no real success fail the run (`ErrUnavailable`) | `TestCircuitBreakerAfterTwoDeadBatches` |

Other tests:
- `TestRunCreateAndUpdateWriteVersionsAndEvidence`
- `TestConsolidateSchemaInjection`
- `TestRunObservationCap`
- `TestRunDropsInvalidActions`
- `TestRunEmptyPayloadDisabledAndBadPayload`
- `TestRunManualScopes`
- `TestModelReaches`
- `TestStrategyMatch`
- `TestStrategyPreview`
- `TestInvalidatorClearForMemory`

All tests run on `internal/fake` with the real config resolver and router. Test-only decorators wrap fake
ports: a Checkpointer hook, an Embedder hook and a recording Store.

## Decisions and CCRs
- **1000 — run shape.**
  - `Run` enqueues its own continuation, so **WP-14's handler must not re-enqueue on More**.
  - Nothing is resurrected or trimmed.
  - Stamps re-check skipped scopes.
  - The cap also applies to the shared scope.
  - `Usage` is added to the result.
- **1001 — related observations and dedup.** They read `port.Reader` directly; dedup runs before the tx.
- **1002 — halving and parking.** A health probe tells poison from an outage; an outage parks nothing.
- **1003 — unit scope mode.** Item column (WP-15) > overrides > run default > combined.
- **1004 — refresh triggers and patterns.**
  - Payload `{"model_id":...}`, serial key `mm:{id}`, enqueued per writing apply tx.
  - Patterns default to all_strict, with no globs.
- **1005 — ClearScopes doesn't enqueue.** The caller does.
- **CCR WP-10-1.** Merged into the WP-15 batch (#6, #7, #8, #20, #21, #22).

## Wiring notes for WP-14
- Build with `NewConsolidator(ConsolidatorDeps{...})`. Required:
  - `Store`, `Config` (`config.NewResolver(store, consolidate.NewStrategyMatcher(scopeLister), server)`),
    `Router`, `LLM`, `Embedder`, `Clock`;
  - `Counter`, needed by any scope with a cap.

  Optional:
  - `Models`: nil means no refresh triggers;
  - `DocScopes`: `Document.ObservationScopes` once WP-15 lands;
  - `Tokens`;
  - `Checkpoint`: `worker.NewCheckpoint`.
- **The consolidate handler must NOT re-enqueue on More** (decision 1000). Fix the stub comment in
  `internal/worker/handlers.go:36` then. It passes `res.Usage` to `Complete`.
- **ClearScopes callers enqueue consolidation themselves** when they reconsolidate (decision 1005).

## Known gaps
- Dedup covers creates only. An updated observation that drifts next to a twin is not merged: there is
  no single-observation delete.
- The consumer interfaces in `extras.go` have no production implementation until WP-15/WP-14. Until then:
  capped scopes fail their run, Preview fails, no refreshes are triggered and per-item scopes are
  unread.
- The Postgres `MarkConsolidated` stamps unconditionally. An edit that commits between our re-read and
  our stamp can still be overwritten; a seq-conditional stamp belongs in the adapter follow-up.
- `ClearForMemory` resets the cleared unit's co-sources, not the unit itself (WP-13 resets it when
  reconsolidating).
- Related-observation lookup bypasses the cross-encoder (decision 1001). Patterns have no globs (decision
  1004).
