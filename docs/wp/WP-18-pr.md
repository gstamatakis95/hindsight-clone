# WP-18 — Final-review fix-up 1: consolidation → refresh, scope stamping (FX-1 + FX-2)

## Built
- [x] **FX-1 (C1).** Consolidation now enqueues `refresh_mental_model` in the mentalmodel payload shape,
  `{"mental_model_id": ...}`. `consolidate.RefreshPayload` mirrors that shape: consolidate can't import
  mentalmodel, because the engine AGENTS.md says nothing imports mentalmodel. `mentalmodel.RunJob`
  (`decodeRefreshPayload`) also accepts the legacy `model_id` key for rows already queued; the current key
  wins.
- [x] **FX-2 (H2).**
  - `apply` now returns the facts that the landed actions cite (`applyOutput.cited`), and
    `run.recordCited` adds the scope to `r.cited`.
  - `stampable` runs in apply at the last scope and in `finish`, both under `tx.Seq`. It already re-checks
    `Cites` for every scope in `r.cited`, so it now covers the scopes this run wrote too.
  - A unit that a scope no longer cites stays unstamped. `finish` then finds it unconsolidated, enqueues
    the continuation and reports `More=true`.

No interface, constructor or frozen-path changes. No CCR.

## Acceptance tests
| Accept item | Test | Result |
|---|---|---|
| Wired engines over fakes: a refresh that consolidation enqueued runs through `mentalmodel.RunJob` and writes a version; after the maintenance reconcile (store's own `FailedRefreshes`) the model is not paused | `mentalmodel.TestConsolidationTriggeredRefreshRunsAndStaysUnpaused` | pass (on main it fails with paused=1) |
| Payload wire shape is exactly `mental_model_id` (raw-key check, independent of either struct) | `consolidate.TestRunTriggersRefreshes` | pass |
| Legacy `model_id` rows still run | `mentalmodel.TestRunJobAcceptsLegacyModelIDKey` | pass |
| e2e `ConsolidationTriggersRefresh`: model with refresh_after_consolidation; retain, wait for idle; the version advanced, there is no FAILED refresh op, and the model is not paused after a maintenance tick | `e2e.TestScenarios/ConsolidationTriggersRefresh` | pass (with the fix reverted it fails: `payload {"model_id": "alice"}`) |
| Reviewer's overlay: ClearScopes([a]) between [a] and [b] of a per_tag run. Nothing is stamped, More=true, and the continuation re-creates [a] without re-deciding [b] | `consolidate.TestRunClearedEarlierScopeIsNotStamped`; the reviewer's `b_cosource_test.go` overlay also passes | pass |
| Same, with a co-source reset by invalidate | `consolidate.TestRunCoSourceInvalidatedIsNotStamped` | pass |
| Same, with a co-source reset by retag | `consolidate.TestRunCoSourceRetaggedIsNotStamped` | pass |
| Existing replay, poison and edit tests stay green | `consolidate.TestRunReplayAfterKillAtThirdScope`, `TestRunHalvesDownToPoisonUnit`, `TestRunEditBetweenScopesIsNotStamped`, `TestRunRecheckCitedScopesBeforeStamp` (whole package) | pass |
| rules.md points at the new tests | rows "consolidate: up to 100 oldest units…" and "consolidate: triggers refreshes…" | done |

Gates: `make gen` leaves no diff, and `make lint BREAKING_AGAINST=contracts-v1`, `make test` and
`make e2e` all pass. No compose projects are left behind.

## Decisions and CCRs
- `docs/decisions/1800-refresh-payload-one-shape.md`: one payload shape. It supersedes the payload line of
  decision 1004.
- `docs/decisions/1801-stamp-recheck-run-cited-scopes.md`: re-check the scopes this run wrote before
  stamping.
- No CCRs.

## Known gaps
- The e2e scenario uses an untagged model and untagged retains, so reach isn't in question.
  Tagged-scope reach is covered by `consolidate.TestRunTriggersRefreshes`.
