# Recall

Recall answers a query with ranked memories (facts and observations) packed into a token budget. It runs on the
api path, within the RPC deadline. `recall.Engine` (`internal/recall`) is a library: the Recall RPC, reflect's
retrieval tools, single-shot mental-model refreshes and the mental-model scope compiler all use it. Consolidation
does not go through the engine; it calls the vector and text searches of the store directly (see
[consolidation.md](consolidation.md#related-observations-and-dedup)).

```
parse (embed, time window, filters) ─┬─ vector   (HNSW)    ─┐
                                     ├─ text     (BM25)    ─┤
                                     ├─ graph    (links)   ─┼─ RRF fuse (k=60) ─ cut to rerank window (300)
                                     └─ temporal (window)  ─┘   ─ re-check ─ cross-encoder rerank ─ boosts
                                                                  ─ floors ─ prefer_observations ─ pack ─ includes ─ stale
```

The selected arms run concurrently, each as its own query with no shared snapshot: a write that lands mid-recall
may appear in one arm and not another, which fusion tolerates.

## Query parsing

- A blank query is INVALID_ARGUMENT; an unknown or deleting bank is NOT_FOUND (config resolution fails first). A
  query over 500 tokens is cut to 500 (reflect sends model-written queries).
- The query is embedded with the server-wide embedding model.
- **Time window.** An explicit `time_window_start/end` disables query time parsing. Otherwise time normalization
  ([retain.md](retain.md#time-normalization)) derives a window from the query relative to `query_time` (default
  now); no cue means no window and no model call; any parsing failure degrades to no window. The window only
  feeds the temporal arm and the temporal boost; it never filters results.
- **Fact types** (`fact_types`) default to every type; observations are a fact type.
- **Tags** are one `TagFilter`: flat `tags` under a `match` mode, or boolean `groups`; setting both is
  INVALID_ARGUMENT. The filter is compiled once into a predicate every arm shares.

## Tag filters

Tags are normalized before matching.

| Mode | Matches |
|---|---|
| `TAGS_MATCH_ANY` (default for Recall and Reflect) | the memory shares a tag, or is untagged |
| `TAGS_MATCH_ALL` | the memory has every tag, or is untagged |
| `TAGS_MATCH_ANY_STRICT` (default for a tag-group leaf) | the memory shares a tag; untagged excluded |
| `TAGS_MATCH_ALL_STRICT` | the memory has every tag; untagged excluded |
| `TAGS_MATCH_EXACT` | tag set equality; an empty exact filter selects only untagged memories |

- An empty filter means no filter in every mode except exact. An empty non-exact leaf is no filter too.
- Tag groups are a tree: `leaf`, `all_of` (empty = true), `any_of` (empty = false), `none_of`. Repeated top-level
  groups are AND-ed. A malformed node matches nothing.
- **Fuzzy leaves** (`resolve = RESOLVE_FUZZY`) resolve each token against the bank's tag vocabulary (all memory
  tags, by count) with trigram similarity on the value part, only within the same `namespace:` prefix, at or
  above `recall.fuzzy_tag_min_similarity` (default 0.45). An exact hit wins; a token with no match stays as itself
  (the leaf becomes unsatisfiable, never "no filter"). Any = the union of the expansions; all = the AND of
  per-token unions; exact = an OR over the cross product, where more than 32 branches is INVALID_ARGUMENT.
- `domain.MatchTags`, `MatchGroup` and `MatchFilter` are the reference semantics; the Postgres rendering of a
  `domain.TagPredicate` agrees with them.

## Arms

An arm is one retrieval strategy behind the `recall.Arm` interface: a name, a one-line description, and
`Retrieve(ctx, Query)` returning at most `Query.Limit` hits of the bank, best first. The engine knows arms only by
name; it never special-cases one. The built-in arms live in `internal/recall/arms`, one file each, and are
registered in `cmd/memd/wire_services.go` in this order, which is also the fusion order:

| Arm | Query | Raw score | Floor (`min_scores.arms`) |
|---|---|---|---|
| `vector` | HNSW cosine over the query embedding ([storage.md](storage.md#search-query-shapes)) | cosine | absent = 0.3; an explicit 0 disables it |
| `text` | BM25 over text and context | BM25 | absent = none |
| `graph` | Seeds: its own vector search for the top 20 units at cosine ≥ 0.3. Then 1..`recall.graph_max_hops` (default 1, clamped 1–2) forward hops through `graph.Reader.Expand` over **semantic and causal** links only | best product of link weights | ignored |
| `temporal` | Units overlapping the time window, spread over `recall.temporal_buckets` (default 4) sub-windows, kept only with cosine ≥ 0.1 to the query (units without an embedding drop out) | proximity, used only to order the arm | ignored |

- The temporal arm returns `recall.ErrSkip` when there is no window, so it is left out of fusion and the trace.
  It is a `recall.WindowArm`: its hits get the temporal boost.
- **Selecting arms.** `RecallRequest.arms` (and `ReflectRequest.arms` for reflect's recall and
  search_observations tools) names the arms to run; empty = the effective `recall.arms`, else every registered
  arm. An unknown or repeated name is INVALID_ARGUMENT, and so is an unknown name in `min_scores.arms`,
  `recall.arms` or `recall.arm_boosts` anywhere a config is accepted. `RecallOverrides.recall.arms` must stay
  empty: the request's own field picks the arms. `ListRecallArms` lists the registered names and descriptions.
- **Settings per arm** are keyed by arm name, never a field per arm: `recall.arms`, `recall.arm_boosts`,
  `min_scores.arms`, and `Scores.arms` and `RecallTrace.arms` in the response. Each arm reads its own entries with
  `Query.ArmSettings`.
- **The re-check.** After fusion the engine hydrates the kept ids with `GetUnits` and keeps only live units of the
  request's bank that pass its fact types, tag predicate and seq bound, whatever an arm returned. This is the
  strict boundary that lets an arm (such as a future graph-database reader) treat filters as hints. The built-in
  arms filter natively, so for them the re-check drops only units changed since retrieval.
- Any arm error other than `ErrSkip` fails the recall.

### Adding an arm

1. Implement `recall.Arm` in its own file under `internal/recall/arms` (or its own package). `Retrieve` honours
   ctx, never writes, and returns `ErrSkip` when it does not apply. Implement `WindowArm` only if its hits are
   time-window matches that deserve the temporal boost.
2. Give it a constructor that takes only the boundary it reads (`store.Search`, `graph.Reader`, `ai.Embedder`,
   ...). Arms never import the engine's internals or one another.
3. Register it in `recallArms` in `cmd/memd/wire_services.go`; its position is its fusion order.
4. Run the contract suite `internal/recall/armtest` from its tests on pgtest.

Clients then select it by name, configure it (`recall.arms`, `recall.arm_boosts`, `min_scores.arms`) and see it in
ListRecallArms, traces and `Scores.arms`. `internal/recall/doc.go` has the full recipe.

## Budgets

The request's `budget` sets the **per-arm candidate cap**, never the token budget.

| Budget | Fixed (default) | Adaptive |
|---|---|---|
| `BUDGET_LOW` | `recall.budget_low` = 100 | `round(level × max_tokens / 4096)` clamped to [`budget_min` 20, `budget_max` 2000] |
| `BUDGET_MID` (default when unset) | `recall.budget_mid` = 300 | same |
| `BUDGET_HIGH` | `recall.budget_high` = 1000 | same |

`recall.budget_function` picks fixed or adaptive. An inverted clamp uses `budget_min`. The cap is global across
fact types (one query per arm, not one per type).

## Fusion

- RRF: `score = Σ over arms 1 / (k + rank)`, k = `recall.rrf_k` (default 60), summed in the fixed arm order so
  float sums are deterministic. Ties keep first-seen order.
- `recall.arm_boosts` (per arm name, absent or ≤ 0 = 1) divide that arm's rank, `1 / (k + rank / w)`, and are used
  **only** to choose which candidates survive the `recall.reranker_max_candidates` cut (default 300), and only
  when the pool exceeds it. The stored RRF score stays unboosted. (Weighted RRF `w / (k + rank)` collapses recall
  once w exceeds RRF's dynamic range.)
- Only the survivors of the cut are hydrated, re-checked, scored and returnable; the tail is dropped.

## Rerank

- When `recall.rerank` is on, the cross-encoder (RERANK slot) scores each survivor; the document text is
  `[Date: June 05, 2022 (2022-06-05)] context: text`. Scores are normalized to [0, 1] by the gateway adapter; an
  unscored or NaN document gets 0. A reranker error fails the recall, and `recall.rerank` on with no RERANK
  binding is FAILED_PRECONDITION (`MODEL_UNBOUND`).
- With rerank off, the base score is the passthrough of the **unboosted** RRF order, linear from 1.0 down to 0.1.
- The server level turns `recall.rerank` off by default when `defaults.models.rerank` is unbound; a level that
  binds RERANK should also set `recall.rerank = true`.

## Boosts

Multiplicative, around neutral 0.5:

`final = base × (1 + recency_alpha·(recency − 0.5)) × (1 + temporal_alpha·(temporal − 0.5)) ×
(1 + proof_alpha·(proof − 0.5))`, alphas default 0.2 / 0.2 / 0.1.

- **Recency:** linear decay over `recall.recency_horizon_days` (default 365) down to a 0.1 floor, aged from
  occurred start, else mention time, else occurred end. Timeless and undated memories are neutral. A date covering
  a whole calendar month or year is aged from its end and capped at neutral.
- **Temporal:** only for candidates a `WindowArm` (temporal) returned; others are neutral:
  `1 − min(|best − mid| / (window days / 2), 1)`, where best is the occurrence midpoint (else start, end, or
  mention time) and mid the window midpoint. An undated memory gets 0.5; a zero-length window gives 1. A window
  with one open bound anchors on the set bound with `recency_horizon_days` as the half-width.
- **Proof:** observations only, `0.5 + ln(proof_count) / 10` clamped to [0, 1]; neutral otherwise.
- Ties break by RRF score, then id.

## Floors

- Arm floors (`min_scores.arms`) are passed into the arms' queries.
- `min_scores.reranker` applies only to candidates the cross-encoder scored, compared at float32 precision.
- `min_scores.final` applies to every candidate after boosts.

## prefer_observations

Every observation ranked within the first `2 × per-arm limit` candidates supersedes the raw facts it cites, which
are dropped wherever they rank; the freed budget backfills during packing. An observation below that window drops
nothing.

## Packing

- `max_tokens` = 0 means 4096. Results are walked in rank order; one that doesn't fit is skipped and the walk
  continues, so one oversized result never evicts shorter ones behind it.
- If nothing fits, the top result is returned whole, over budget: an empty answer would read as "nothing known".
- Tokens are counted with the shared cl100k tokenizer.

## Includes

| Include | Behaviour |
|---|---|
| `chunks` | Source chunks of the results, in result order, within `chunks_max_tokens` (default 8192). The chunk that crosses the budget is cut, marked `truncated`, and the walk stops. |
| `source_facts` | For observations, their cited facts, within `source_facts_max_tokens` (default 4096); a fact that doesn't fit is skipped and `source_facts_truncated` is set. Bounded by the request's seq range. |
| `entities` | Entity state of the results: entity id, canonical name, and the live observations attached to the entity or citing a fact attached to it. |

## Stale observations

After packing, recall makes one call over the distinct scopes of the packed observations and marks an observation
`stale` when its scope holds an unconsolidated fact. Facts are never stale. Staleness reads the latest committed
state, not the recall's snapshot; it is advisory.

## Trace

With `trace`, the response carries one `ArmTrace` per arm that ran (name, candidate count, duration, top ids), the
fused, reranked and packed counts, the token budget and tokens used, the time window that parsing resolved, and
the total duration. Every returned memory carries `Scores`: the final score, the reranker score when rerank ran,
and the raw score per arm that returned it.

## Defaults

| Knob | Default |
|---|---|
| `recall.budget_function` | fixed |
| `recall.budget_low / mid / high` | 100 / 300 / 1000 |
| `recall.budget_min / budget_max` | 20 / 2000 |
| `recall.rrf_k` | 60 |
| `recall.reranker_max_candidates` | 300 |
| `recall.rerank` | true when `models.rerank` is bound at the server level, else false |
| `recall.recency_alpha / temporal_alpha / proof_alpha` | 0.2 / 0.2 / 0.1 |
| `recall.recency_horizon_days` | 365 |
| `recall.graph_max_hops` | 1 (clamped 1–2) |
| `recall.temporal_buckets` | 4 |
| `recall.fuzzy_tag_min_similarity` | 0.45 |
| `recall.arms` | empty (every registered arm) |
| `recall.arm_boosts` | none (1 for every arm) |
| `max_tokens` (request) | 4096 |
| `budget` (request) | mid |
| Vector arm floor when unset | 0.3 |
| Graph seeds | top 20 at cosine ≥ 0.3 (not configurable) |
| Temporal relevance floor | cosine 0.1 (not configurable) |
| Max query tokens | 500 |
