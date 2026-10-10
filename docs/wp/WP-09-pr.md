# WP-09 · Recall engine

Branch `wp/WP-09-recall`, rebased on main (99fbdd1). `make gen` leaves no diff. `make lint`
(BREAKING_AGAINST=contracts-v1) and `make test` (`go test -race ./...`) pass. Only
`internal/engine/recall/**` changed, plus its regenerated mocks, decisions 900-902 and CCR WP-09-1.

## Built
- [x] **Parse** (`parse.go`, `tags.go`, `trigram.go`):
  - query embedding;
  - time window: an explicit window skips time analysis; otherwise timex runs, calling TIME_PARSE only for a
    cue its rules can't resolve, and degrading to no window;
  - fact-type filter;
  - flat tags or tag groups compiled to one `port.SQLPredicate` shared by all four arms, with normalized tags
    and an explicit mode (flat tags default ANY, group leaves default ANY_STRICT);
  - fuzzy leaves resolved by pg_trgm-style trigram similarity, following Hindsight's tag_resolution.
- [x] **Four arms run concurrently** (errgroup) (`arms.go`):
  - semantic and keyword take the `min_scores` arm floors;
  - graph runs its own semantic seeds (top 20 at >= 0.3), then expands 1-2 hops;
  - temporal runs only when there is a window, bucketed;
  - `enabled_strategies` selects arms, and empty means all four.
- [x] **RRF** (`fuse.go`): k = 60 from config, a deterministic arm order, ties by id. `strategy_boosts`
  enter in rank space (Hindsight stage 1).
- [x] **Rerank the top `reranker_max_candidates`** (`score.go`):
  - the RERANK slot goes through the router;
  - the document format is Hindsight's;
  - with rerank off, the base score comes from the RRF rank;
  - a reranker error fails the call.
- [x] **Multiplicative boosts** (recency 0.2, temporal 0.2, proof 0.1), each read from config and
  overridable per request. They copy Hindsight's formulas: linear recency over `recency_horizon_days` with
  the coarse-date rule, temporal-arm proximity, and log proof count.
- [x] **Budgets** (`budget.go`): 100 / 300 / 1000. Fixed mode uses them as configured; adaptive mode scales
  them by `max_tokens / 4096` and clamps to [`budget_min`, `budget_max`].
- [x] **`prefer_observations`** (`prefer.go`): drops the raw facts cited by a returned observation and
  backfills, packing to a fixed point.
- [x] **Packing to `max_tokens`** (`pack.go`): items that don't fit are skipped. If nothing fits, the top
  item is returned whole. `max_tokens` 0 means 4096.
- [x] **Includes** (`include.go`), each within its own budget:
  - chunks (default 8192): the chunk that crosses the budget is cut and marked `truncated`;
  - source facts (default 4096): a fact that doesn't fit is skipped and `source_facts_truncated` is set;
  - entity state.
- [x] **The trace:**
  - per arm: candidates, duration and top 5 ids;
  - counts of fused, reranked and packed candidates;
  - token budget and tokens used;
  - the resolved window and the duration.

## Acceptance tests (`internal/engine/recall`, all pass with `-race`)
| Accept item | Test(s) |
|---|---|
| Golden: fusion order | `TestFuseRRFOrder`, `TestFuseStrategyBoostRankSpace`, `TestRecallFusionOrderGolden` |
| Golden: each boost isolated | `TestBoostRecencyIsolated`, `TestBoostTemporalIsolated`, `TestBoostProofIsolated`, `TestBoostNeutral` |
| Golden: tag group logic (all_of / any_of / none_of, strict variants) | `TestRecallTagGroups` (end to end on the fake store), `TestCompileFlatTags`, `TestCompileGroups`, `TestFuzzyResolution`, `TestExpandToken`, `TestTrigramSimilarity` |
| Golden: min_scores at the arm and result level | `TestRecallMinScoresArm`, `TestRecallMinScoresResult`, `TestScoreGate` |
| Golden: prefer_observations backfill | `TestRecallPreferObservationsBackfill`, `TestPreferObservations` |
| Golden: the budget clamp | `TestBudgetLimit` |
| Golden: the packing edge case | `TestPackSkipsOversized`, `TestPackFloorWhenNothingFits`, `TestTruncateTokens` |
| Golden: include budgets reported as truncated | `TestIncludeChunksTruncated`, `TestIncludeSourceFactsTruncated`, `TestRecallIncludes` |
| No-cue queries make no TIME_PARSE call | `TestRecallNoCueNoTimeParse` (real timex, unscripted fake LLM, 0 calls); `TestRecallExplicitWindowNoTimeParse`; positive control `TestRecallCueFallbackCallsTimeParse` (an unresolvable cue makes exactly 1 call and degrades to no window) |
| Seq snapshot also bounds hydration (mental-model refreshes) | `TestRecallSeqSnapshot` |
| Rerank top N; rerank off | `TestRecallRerankTopN`, `TestRecallRerankDisabledUsesRRF` |
| Arms, graph hop, temporal only with a window, enabled_strategies, trace | `TestRecallArmsAndTrace` |
| Unknown bank = NOT_FOUND (before any embed call); blank query = INVALID_ARGUMENT | `TestRecallUnknownBank` |

## Decisions and CCRs
- **900:** recall-owned defaults (recency horizon 365, graph hops 1, temporal buckets 4, fuzzy similarity
  0.45), graph seeds, the adaptive budget formula, `max_tokens` 0, the packing floor, the 500-token query
  cut, and empty `enabled_strategies`.
- **901:** rank-space strategy boosts in RRF, the rerank window, the RRF-rank base with rerank off, the boost
  formulas, floor semantics, and prefer_observations over *returned* observations.
- **902:** the consumer-declared `Lookups` (tag vocabulary, chunks, entity state), fuzzy resolution rules,
  include semantics, and stale not built.
- **CCR WP-09-1 (PROPOSED):** add `BankTags`, `GetChunks`, `EntityStates` and `StaleScopes` to
  `port.Reader`. `recall.Lookups` already uses these exact signatures.

## Interface changes inside the package (not frozen)
- `Fuser.Fuse` takes `boosts map[string]float64`.
- `TagFilterCompiler.Compile` takes `minSimilarity`. `NewTagFilterCompiler` takes a `TagVocabulary`, and
  `NewIncluder(r, lookups, count)` takes the optional `Lookups`.
- `ParsedQuery` gains `Predicate`, `MinScores`, `Seq` and `Config`.
- `Boosts` now holds multipliers.
- `RecallerDeps` is new.
- The exported request and result types the server converts are unchanged.

## Known gaps
- **Fuzzy tags, chunks and entities need CCR WP-09-1** (or an adapter passed as `RecallerDeps.Lookups`).
  Without it, a fuzzy leaf fails with FAILED_PRECONDITION and chunks and entities attach nothing.
- **`Memory.stale` is always false** until StaleScopes exists. It is not on the card.
- **No tokenizer is wired.** `TokenCounter` is injected, so WP-14 must supply a tiktoken-compatible one.
- **Chunks are not filtered by their document's tags** (Hindsight #5030).
- **Hindsight's stage-2 additive strategy boost** after rerank is not built.
- **Calling `Recall` inside `Store.InTx` is not supported.** Its config read opens a short transaction of
  its own, and in the fake that deadlocks.
- **Arm and recall durations in the trace use the injected Clock.** They read 0 under the frozen fake clock.
