# WP-12 · Mental model engine

Branch `wp/WP-12-mentalmodel`, rebased on main (0dcca95). Touches only `internal/engine/mentalmodel/**`,
its regenerated mocks (`internal/mocks/engine/mentalmodel`), `go.mod`/`go.sum` (adds
`github.com/robfig/cron/v3`, on the allowed list), `docs/wp/WP-12*.md`, `docs/ccr/WP-12-1.md` and
`docs/decisions/1200-1205`. No frozen path changed.

Gates in the worktree: `make gen` (no diff), `make lint BREAKING_AGAINST=contracts-v1` (0 issues, ifacedoc,
buf, ruff), `go test -race ./...` (all pass, Postgres suites included).

## Built (card "Build")
- [x] **ChangeDetector** over seq and retracted sources (`detect.go`, `scope.go`): reads BankSeq first,
  looks for in-scope units in `(last_seq_seen, S]`; a tagged model ignores untagged units; any cited
  source that is no longer live makes it stale.
- [x] **Synthesizers**:
  - agentic (`agentic.go`) via the consumer-declared `Reflector`; reflect loop answering as a document,
    the model itself always excluded from the page tools;
  - single-shot (`single_shot.go`) via `Recaller`: one recall with observations first, then one SYNTHESIZE
    (full) or MENTAL_MODEL_DELTA (delta) call. Both delta paths share `planner.go` (decision 1200).
- [x] **Delta ops** on section and block anchors (`markdown.go`): a lossless split into sections and
  blocks with content-derived ids (Hindsight's structured_doc), all eight op kinds, skip reasons, and
  orphan table rows merged. When every op is rejected the model gets one retry showing the rejected ops;
  after that the refresh fails and the document stays. Partially applied ops are kept, so the document is
  never replaced by a partial one.
- [x] **RefreshRunner** (`runner.go`, `write.go`):
  - effective strategy (call > trigger > bank > agentic) and mode, with fallback reasons `force_full`,
    `no_baseline_content`, `source_query_changed`, `sources_retracted`, `max_delta_ratio_exceeded`;
  - reads BankSeq first and sets `last_seq_seen = S` on success. Nothing in scope or in the window means
    no LLM call (NO_NEW_FACTS);
  - `source_ids` accumulate across deltas and are replaced (pruned) on full;
  - a version history entry for every replaced version. Failure entries are written by the reconcile
    (decision 1201), so job retries are not recorded;
  - pause on failure (reconcile); only a successful manual run clears the flag;
  - structured-output projection of the final page, page embedding, `keep_trace`;
  - the write is one short tx with no model call. It returns `ErrConflict` if the model was cleared or
    re-scoped mid-run.
- [x] **RefreshScheduler** (`scheduler.go`): skips paused and fresh models; debounce; interval floor; run
  now otherwise. `EnqueueRefresh` gives the queued-refresh semantics: join, then upgrade (force_full
  OR-ed, strategy and overrides replaced, manual set); a manual request releases a parked job. Cron and
  the other automatic triggers are in `triggers.go` (decision 1204).
- [x] **Dry run** writes nothing (no version, watermark, history, event, usage or seq bump) and returns
  the draft, a unified diff (`diff.go`), the window, the facts counts and `would_persist`.
- [x] **Page search, folders, clear** (`pages.go`, `admin.go`):
  - RRF over term overlap and page-embedding cosine;
  - folder-prefix listing and recursive DeleteFolder;
  - Clear keeps the cleared content in history and makes the next refresh full.
- [x] Carried forward:
  - `RefreshFailureReconciler` (structurally `worker.RefreshFailureReconciler`, keyed on the latest
    terminal op, idempotent via `last_refresh_failed_at`);
  - `Triggers.EnqueueDueCronRefreshes` (structurally `worker.CronRefresher`, due-ness keyed on the fire
    time);
  - `Admin`: `server.MentalModelAdmin` with mirror types (decision 1205; WP-14 adds a conversion shim);
  - `RunJob` for the worker's refresh handler.

## Acceptance tests
| Card "Accept" | Test | Result |
|---|---|---|
| A late-committing writer is picked up by the next refresh | `TestLateCommittingWriterPickedUpByNextRefresh` | pass |
| An untagged write never stales a tagged model | `TestUntaggedWriteNeverStalesTaggedModel` | pass |
| A delta with all ops rejected keeps the document | `TestDeltaAllOpsRejectedKeepsDocument` (agentic and single-shot), `TestApplyEditsAllRejectedAndEmpty` | pass |
| Nothing in scope means no LLM call | `TestNothingInScopeMeansNoLLMCall` (agentic and single-shot) | pass |
| Pause and manual unpause | `TestPauseAndManualUnpause` | pass |
| Joining a queued refresh with `force_full` upgrades it | `TestJoiningQueuedRefreshWithForceFullUpgrades` | pass |
| Both strategies produce a version through the same runner | `TestBothStrategiesProduceVersionThroughRunner` | pass |

Supporting tests:
- refresh: `TestDeltaRetrySucceedsAndSourcesAccumulate`, `TestModeFallbackReasons`, `TestDryRunWritesNothing`,
  `TestRefreshConflictWhenModelChanged`, `TestStructuredOutputProjectedFromFinalPage`,
  `TestMaxDeltaRatioFallsBackToFull`;
- scheduling and triggers: `TestDecide`, `TestCronRefreshes`, `TestRetractedSourceTriggersRefresh`,
  `TestRunJob`;
- admin and pages: `TestCreateMentalModelValidatesAndQueuesBuild`, `TestAdminGetDetailAndStale`,
  `TestAdminUpdateResetsWatermarkOnScopeChange`, `TestAdminClearKeepsHistory`, `TestFoldersListAndDelete`,
  `TestSearchPages`;
- markdown and diff: `TestSplitMarkdown*`, `TestSplitRenderRoundTrip`, `TestApplyEdits*`,
  `TestChangedFraction`, `TestDiffMarkdown*`.

## Decisions and CCRs
- CCR **WP-12-1** (PROPOSED): adds four reads to `port.Reader`: `NewestSeqInScope`, `ListMentalModels`,
  `CronModels`, `FailedRefreshes`. The engine consumes them through `reads.go`; tests use stubs over the
  fake.
- 1200 synthesizer shape: Delta returns a draft, `ModelDeps`, how the agentic delta works.
- 1201 refresh failures and pause.
- 1202 modes, fallbacks, watermark and the version write.
- 1203 staleness scope: untagged units, fuzzy leaves.
- 1204 queue semantics, triggers and cron.
- 1205 admin mirror types, folders, clear, history and page search.

## Known gaps
- **CCR WP-12-1 reads are not implemented** in the fake or Postgres. Until WP-15 lands, WP-14 can't wire
  the detector, runner pre-check, listing, cron or reconcile.
- **Refresh context for the agentic loop.** `reflect.ReflectRequest` (WP-11) has no free-form context
  field, so the `mm_refresh_context` prompt (full vs delta advice) isn't passed to the loop. Suggested
  fix: WP-11/WP-14 add `Context string` and the runner renders `prompts.MentalModelRefreshContext`.
- **Agentic overrides.** The agentic synthesizer passes the merged effective config as
  `ReflectRequest.Overrides`. This is fine as long as the reflect engine doesn't re-validate overrides
  against the Reflect RPC allowlist.
- **Retrieval counts.** For agentic runs, `facts_retrieved` equals the number of cited memories
  (`ReflectResult` has no retrieval count).
- **Approximate budgets.** Document and output token budgets in the delta prompt use a 4-bytes-per-token
  estimate.
- **Fuzzy tag leaves** are matched as exact names in the staleness check only (decision 1203).
- **Cron race.** A cron refresh can be enqueued a second time if another worker claims the first between
  two ticks. That costs one NO_NEW_FACTS run with no LLM call (decision 1204).
- **Page search** scores pages in memory over one listing; fine for tens to hundreds of pages per bank.
