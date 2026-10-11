# 1601 — Recall on the batch ports

## Context
With CCR WP-09-1 and WP-09-2 applied, `port.Reader` has `TagCounts`, `GetChunks`, `EntityStates`,
`StaleScopes` and the temporal relevance filter. The optional `Lookups` dependency existed only because
these reads were missing.

## Decision
- **`RecallerDeps.Lookups` is removed.** `Reader` serves everything, so WP-14 wires nothing extra.
  `NewIncluder(r, count)` drops its `Lookups` argument.
- **`TagVocabulary` keeps only the `TagCounts` method.** It is the port signature, so `port.Reader` and the
  mental-model engine's scope reader both satisfy it.
  - The compiler reads all memory tags (`limit 0`) and sorts them by count descending, then by tag.
  - It does this only when a fuzzy leaf is present.
  - An unknown bank cannot reach it: config resolution fails first.
- **`Memory.Stale`:** after packing, recall calls `StaleScopes` once, over the distinct scopes
  (`NewScopeKey(bank, tags)`) of the packed observations. The flag is set on observations only and is
  always false on facts.
  - It reads the latest committed state, not the request's seq snapshot. Stale is advisory.
- **Temporal arm:** it sends `Embedding = q.Embedding` and `MinSimilarity = 0.1`, Hindsight's in-window
  relevance pool. So in-window units that are irrelevant (cosine < 0.1) or that have no embedding are
  no longer temporal hits.
  - `TestRecallExplicitWindowNoTimeParse` now uses a unit that shares words with its queries.

## Alternatives
- Keeping `Lookups` and defaulting it to `Reader`: it is a second name for the same dependency, so it was
  dropped.
- Filling Stale per result with one call each: N round trips instead of one.

## Spec lines involved
SPEC 447 (fuzzy tags), 492-493 and 544-545 (include chunks/entities), 536 (Memory.stale), 2023 (temporal
arm); CCR batch #1-#5 and #27.
