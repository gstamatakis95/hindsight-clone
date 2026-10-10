# 900 — Recall defaults, budgets and packing

## Context
The spec lists recall's RecallConfig fields but leaves several defaults and edge cases open:
- knobs with no server default (decision 400 / WP-04: "the reading engine owns those defaults");
- the adaptive budget formula (RecallConfig has no adaptive ratios);
- `max_tokens = 0`;
- what packing does with an item that doesn't fit.

## Decision
- **Defaults recall owns:**
  - `recency_horizon_days` 365 (Hindsight's linear window);
  - `graph_max_hops` 1 (Hindsight's direct link expansion), clamped to 1-2;
  - `temporal_buckets` 4;
  - `fuzzy_tag_min_similarity` 0.45 (Hindsight's MIN_SIMILARITY).

  The defaults the spec states (rrf_k 60, reranker_max_candidates 300, alphas 0.2 / 0.2 / 0.1, budgets
  100 / 300 / 1000, clamp 20-2000) are applied too when a config leaves them unset.
- **Graph seeds:** the graph arm runs its own SearchVector for the top 20 units at cosine >= 0.3 (Hindsight's
  GRAPH_SEED_LIMIT and graph_seed_min_similarity). That keeps all four arms concurrent. These are
  constants, not knobs.
- **Budget:**
  - Unspecified means MID.
  - Fixed returns the configured level as is, with no clamp.
  - Adaptive returns `round(level * max_tokens / 4096)` clamped to `[budget_min, budget_max]`. This equals
    Hindsight's adaptive ratios (0.025 / 0.075 / 0.25 of max_tokens, chosen to match the fixed levels at
    4096 tokens) and still lets the configured levels scale it.
  - An inverted clamp uses budget_min.
- **`max_tokens` 0** means 4096: the proto says "the server's default packing budget". Include budgets
  default to Hindsight's 8192 (chunks) and 4096 (source facts).
- **Packing:**
  - Walk in rank order. Skip an item that doesn't fit and keep going, so an oversized item never evicts
    shorter ones behind it.
  - If nothing fits, return the top item whole and over budget (Hindsight's floor in fact_budget.py). An
    empty answer would read as "nothing known".
- **Query:** a blank query is INVALID_ARGUMENT. A query over 500 tokens is cut to 500, because Hindsight
  truncates rather than rejects and reflect sends queries the model wrote.
- **enabled_strategies:** empty (nil or an explicit clear) runs all four arms. Unknown names are ignored.

## Alternatives
- Rejecting a long query would break reflect when the model writes a long tool query.
- Stopping packing at the first overflow (the skeleton's doc) loses short, relevant items that sit behind one
  long one.

## Spec lines involved
RecallConfig and RecallRequest comments (gRPC surface); Recall pipeline; Model routing (TIME_PARSE).
