# 1000 — Shape of a consolidation run

## Context
The spec gives `Consolidator.Run`, `decide` (LLM outside any tx) and `apply` (one short tx). It leaves
several things open:
- who enqueues the continuation;
- what a manual run's scopes select;
- where the completion event and refresh triggers go;
- what happens when observations are disabled;
- what happens when an update's observation disappears between decide and apply.

## Decision
- **Order.** `plan` reads the run in one read-only tx:
  - up to `consolidation_max_units_per_run` oldest unconsolidated units;
  - each unit's scopes;
  - which scopes already cite the unit (skipped).

  Scopes are then processed in `ScopeKey.String()` order. Each runs in LLM batches of
  `consolidation_llm_batch_size`, with a checkpoint before every batch.
- **Stamp.** A unit is stamped in the apply tx of its last pending scope. A unit with no pending scope
  (every scope already cites it, e.g. a replay) is stamped in the final tx.
- **Implicit none.** A batch fact that no action cites is decided "none". It is stamped and gets no
  evidence, so a replay decides it again in that scope, which is harmless.
- **Final tx.** It calls `tx.Seq`, stamps leftovers, then checks for remaining units with
  `Unconsolidated(scopes, 1)`. If any remain it enqueues the continuation. It then appends
  `CONSOLIDATION_COMPLETED` (OperationID = the job). Refresh triggers are not here: each apply tx that
  writes enqueues them (decision 1004), so a run that fails later keeps them.
- **Run enqueues its own continuation.** It does so in the final tx with `NewJob(bank, scopes,
  overrides)`, so it follows the consolidate dedupe rule: `DedupePending` for the automatic job,
  `DedupeNone` when it carries scopes or overrides (review M2; SPEC Jobs table). `More` is informational.
  **WP-14's handler must not re-enqueue on More**: a job with scopes or overrides never dedupes, so a
  second enqueue would add a second row.
- **Manual scopes select units only.** A scoped run takes units whose tags contain one of the scopes (as
  `Unconsolidated` filters). It still decides each unit in every scope it belongs to before stamping it.
  Deciding only the requested scopes would stamp the unit and strand its other scopes.
- **Disabled observations.** With `enable_observations=false` (effective, request included) the run
  does nothing and stamps nothing, so enabling observations later consolidates the backlog.
- **Nothing is resurrected or trimmed (review H2/H3).** The apply tx re-reads the batch's facts. A fact
  that is gone, or whose seq or tags differ from the plan (edited, retagged), is not live. An action is
  skipped whole when any fact it cites is not live, or when its update target is gone or left the scope.
  It is never rewritten as a create or trimmed to its live evidence. When any action was skipped (or
  decide dropped a second update of one observation, review M3), no fact of the batch is stamped, and
  those facts are left out of the rest of the run; the next run re-decides them (scopes that already
  cite them are skipped). A non-live fact is never stamped.
- **Stamps re-check skipped scopes (review M1).** Before stamping, the tx checks that every scope the plan
  skipped because it cited the unit still cites it; if a concurrent clear removed that observation, the
  unit is not stamped.
- **Adapter carry-over.** The Postgres `MarkConsolidated` stamps unconditionally. A concurrent edit that
  commits between our re-read and our stamp (READ COMMITTED) can still be overwritten. A follow-up should
  make the stamp conditional on seq (CCR batch / WP-15).
- **Cap.** The observation cap applies to every scope, the shared one included. This is the simplest
  reading of "per-scope cap"; Hindsight exempts untagged observations.
- **Result.** `ConsolidationResult` gains `Usage` (per slot and model, zero entries skipped) for
  `Claimer.Complete`.
- **Checkpoint.** `ConsolidatorDeps.Checkpoint` gives a run its `Checkpointer`; `worker.Checkpoint`
  satisfies it.
- **Constructors.** These differ from the spec's listing because the frozen ports lack reads (CCR
  WP-10-1):
  - `NewConsolidator(ConsolidatorDeps)`;
  - `NewDeduper(llm, router, port.Reader)` (decision 1001);
  - `NewStrategyMatcher(ScopeLister)`;
  - `NewInvalidator(ObservationClearer)`.
- **Invalidator.** It takes `tx.Seq`. `ClearForMemory` and `ClearDocumentUnits` enqueue the automatic
  consolidate when a clear changed anything; `ClearScopes` enqueues nothing (decision 1005). A nil
  `ObservationClearer` fails every method with FAILED_PRECONDITION. `ClearForMemory` resets the *other* sources only (as `DeleteCiting` does). The cleared unit
  stays stamped unless its caller resets it, which edit and retag do through `Update` /
  `SetDocumentTags`.

## Alternatives
- Leave the continuation to the handler, as the spec's worker signature suggests. Rejected: committing
  it with the final tx means a crash between Complete and the enqueue can't lose it. The maintenance tick
  would still recover the bank, but a manual run's scopes and overrides would be lost.
- Stamp units when observations are disabled. Rejected: it silently discards the backlog.

## Spec lines involved
Engines (Consolidator), Function signatures (decide/apply, NewInvalidator, NewDeduper,
NewStrategyMatcher), Jobs (consolidate row, Bounded runs), Behavioural rules (Change sequence), Events.
