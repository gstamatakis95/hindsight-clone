# 1003 — Where a unit's scope mode comes from at consolidation time

## Context
`RetainItem.observation_scopes` picks an item's scope fan-out, but `memory_units` has no column for it,
and consolidation runs later as a separate job. Hindsight stores `observation_scopes` per memory.

Round 1 read it from `Document.Overrides.DefaultObservationScopes`. The orchestrator ruled against that
(CCR batch #24): ReprocessDocument replaces the stored overrides, so the item's scopes would be lost.
WP-15 adds a nullable `documents.observation_scopes` column, exposed as
`domain.Document.ObservationScopes *ObservationScopes`.

## Decision
- **Precedence.** A unit's scope mode comes from, in order:
  1. its document's item-level `observation_scopes` (WP-15's column), read through the consumer
     interface `DocumentScopeReader` (`ConsolidatorDeps.DocScopes`) until WP-15 lands;
  2. its document's retain overrides, `Document.Overrides.DefaultObservationScopes`;
  3. the run's effective `default_observation_scopes` (server < tenant < bank < request);
  4. `combined`.

  With `DocScopes` nil, step 1 is skipped. WP-14 wires it as `tx.Documents().Get(...).ObservationScopes`
  once WP-15 lands. WP-08 writes the column; it no longer needs to fold the item's scopes into the
  overrides.
- **Expand fallback.** If `Expand` fails at consolidation time, the run uses the unit's combined scope
  rather than stranding the unit. This only happens if `ALL_COMBINATIONS` exceeds `max_combinations`
  because the mode or cap changed after retain, which rejects the case synchronously.
- **Expand semantics.**
  - **Untagged units:** an untagged unit has one scope, the shared one, in every mode except custom.
  - **Custom:** custom sets are used as given (normalized and deduplicated); an empty custom list means
    combined.
  - **All combinations:** ALL_COMBINATIONS lists the non-empty subsets by size, then lexicographically.
    More than `max_combinations` (default 15) is `ErrTooManyScopes`; a `max_combinations` <= 0 is
    INVALID_ARGUMENT.
  - **Order:** scopes are sorted by their key encoding, so every run decides them in the same order.

## Alternatives
- Fold the item's scopes into the document overrides (round 1). Rejected by the orchestrator: lost on
  reprocess.
- A column on `memory_units`. Rejected: one item is exactly one document.

## Spec lines involved
RetainItem.observation_scopes, BankConfig.default_observation_scopes ("for items that set none"),
ReprocessDocument (replaces stored overrides), ScopeExpander, Behavioural rules (ALL_COMBINATIONS
rejected synchronously).
