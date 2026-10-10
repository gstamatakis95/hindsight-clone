# 1003 — Where a unit's scope mode comes from at consolidation time

## Context
`RetainItem.observation_scopes` picks an item's scope fan-out, but `memory_units` has no column for it,
and consolidation runs later as a separate job. Hindsight stores `observation_scopes` per memory.

## Decision
- **Precedence.** The scope mode of a unit comes from three places, in this order:
  1. its document's retain overrides, `Document.Overrides.DefaultObservationScopes`;
  2. the run's effective `default_observation_scopes` (server < tenant < bank < request);
  3. `combined`.
- **WP-08 must store the item's mode on the document.** When an item sets `observation_scopes`, WP-08
  stores it there. One item is one document, and reprocessing replays the overrides, so the mode survives
  ReprocessDocument. Without this, per-item modes fall back to the bank default.
- **Expand fallback.** If `Expand` fails at consolidation time, the run uses the unit's combined scope
  rather than stranding the unit. This only happens if `ALL_COMBINATIONS` exceeds `max_combinations`
  because the mode or cap changed after retain, which rejects the case synchronously.
- **Expand semantics.**
  - **Untagged units:** an untagged unit has one scope, the shared one, in every mode except custom.
  - **Custom:** custom sets are used as given (normalized and deduplicated); an empty custom list means
    combined.
  - **All combinations:** ALL_COMBINATIONS lists the non-empty subsets by size, then lexicographically.
    More than `max_combinations` (default 15) is `ErrTooManyScopes`.
  - **Order:** scopes are sorted by their key encoding, so every run decides them in the same order.

## Alternatives
- A CCR adding an `observation_scopes` column to `memory_units`. Rejected: the document already carries
  the item's overrides, and one item is exactly one document.

## Spec lines involved
RetainItem.observation_scopes, BankConfig.default_observation_scopes ("for items that set none"),
ScopeExpander, Behavioural rules (ALL_COMBINATIONS rejected synchronously).
