# 1001 — Related observations and dedup read port.Reader directly; dedup runs before the tx

## Context
- The spec says consolidation calls the Recaller with type filters.
- It gives `Deduper.Reconcile(ctx, tx, scope, changed)` with `NewDeduper(llm, router, rc Recaller)`.
- `Reconcile` takes a tx but makes a DEDUP_CHECK LLM call, and the spec also forbids model calls inside
  `Store.InTx`.
- `dedup_threshold` is a cosine similarity, which fused recall scores (RRF, rerank, boosts) are not.
- Recall (WP-09) is built in parallel.

## Decision
- **Related observations.** For each fact, consolidation runs the semantic arm (`SearchVector` with the
  fact's embedding) and the keyword arm (`SearchText`) over observations of exactly this scope (an
  `exact` tag predicate). It interleaves them and takes 10 per fact, then pools the batch's union in
  first-seen order.
  - There is no reranker. Hindsight also switched consolidation recall to round-robin interleave, because
    rerank buried the near-identical twin.
  - The shown observations carry up to 20 source facts each. These are cut to the per-observation token
    cap (default 256) and the call's `consolidation_source_facts_max_tokens` (unset = none).
- **Deduper is `Check(ctx, scope, cfg, text, emb)`.** It runs during decide, with no tx open. It probes
  the scope's nearest observation with `SearchVector` (`MinSimilarity` = threshold, top 5). When one is at
  or above `dedup_threshold` it asks DEDUP_CHECK.
  - A threshold >= 1 disables dedup. Unset means 0.97.
  - **Merge** turns the create into an update of the twin with the merged text. The update is re-embedded,
    the twin gains the new fact's evidence and a version, and no new observation is written.
  - **Keep** leaves the create.
  - A failed DEDUP_CHECK keeps the create, so nothing is lost, unless the run is stopping.
- **Creates only.** Dedup checks creates only. Hindsight also folds an *updated* observation into a
  drifted twin and deletes the updated row. memd has no port to delete one observation, so that path is a
  known gap.
- **Exact-text duplicates.** A create whose text exactly equals a shown observation becomes an update of
  it.

## Alternatives
- **Use `recall.Recaller`.** Rejected for now:
  - its scores aren't cosine;
  - it adds rerank cost to every fact;
  - WP-09 isn't merged.

  Switching later only changes `related()`.
- **Keep `Reconcile(tx, ...)`.** Rejected: it would need an LLM call inside the tx.

## Spec lines involved
Engines (Deduper, "Consolidation and mental-model synthesis call the same Recaller"), AGENTS.md (no model
calls inside Store.InTx), BankConfig `dedup_threshold`,
`consolidation_source_facts_max_tokens[_per_observation]`.
