# 1800 — One refresh_mental_model payload shape

## Context
Final review B, C1. Consolidation enqueued `refresh_mental_model` with `{"model_id": ...}` (decision 1004).
The refresh handler, `mentalmodel.RunJob`, decodes `mentalmodel.RefreshPayload`, whose key is
`mental_model_id` (decision 1204). Every refresh that consolidation triggered therefore failed at once
with INVALID_ARGUMENT. The maintenance reconcile then paused the model, so automatic refresh never worked.
It only worked by accident when a manual refresh was already queued and the payload merge added the
right key. `consolidate.TestRunTriggersRefreshes` decoded the payload with consolidate's own struct,
so it could not catch the drift.

## Decision
- **One wire shape**, mentalmodel's: `{"mental_model_id": ..., "force_full"?, "strategy"?, "manual"?}`.
  This supersedes the payload line of decision 1004.
- **No shared type.** The engine AGENTS.md says nothing imports mentalmodel, so consolidate can't call
  `mentalmodel.EnqueueRefresh` or use its type. `consolidate.RefreshPayload` keeps its one field and now
  marshals it as `mental_model_id`. It still carries no flags, so a joined queued row keeps its own
  values under `Queue.Upgrade`.
- **Legacy key.** `RunJob` also reads `model_id` when `mental_model_id` is missing. That covers rows
  queued before this fix that are still pending. The current key wins when both are present. The
  exported `mentalmodel.RefreshPayload` is unchanged, so `EnqueueRefresh` never writes the old key.
- **Guards against drift:**
  - `consolidate.TestRunTriggersRefreshes` decodes the raw payload as a map and requires exactly the key
    `mental_model_id`. That check doesn't depend on either package's struct.
  - `mentalmodel.TestConsolidationTriggeredRefreshRunsAndStaysUnpaused` wires the real Consolidator, the
    real RunJob and runner, and the reconciler over the fake store's own `FailedRefreshes`. The test
    file imports consolidate; that is test-only, and consolidate imports only recall, so there is no
    cycle.
  - `e2e.TestScenarios/ConsolidationTriggersRefresh` covers the deployed path.

## Alternatives
- **Accept only `model_id` in RunJob.** Rejected: that leaves two shapes for one job kind.
- **Move the payload type to a neutral package.** Rejected: `internal/domain` and `internal/port` are
  frozen, and a new shared engine package for one struct is more than the fix needs.

## Spec lines involved
Jobs table (refresh_mental_model serial key `mm:{id}`, consolidate "triggers refreshes of stale models"),
Behavioural rules (Mental models: paused by failure).
