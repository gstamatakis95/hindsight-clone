// Package recall is memd's retrieval core: [Engine] answers the Recall RPC and serves reflect (its recall,
// search_observations and page tools) and the mental-model refreshes, so it never imports those packages.
//
// # Where it sits
//
// recall is a library: it imports domain, the store, graph and ai boundaries, config and timex, and never a
// service, an adapter, gen, pgx, net/http, the S3 SDK or Datadog (depguard). Everything it reads goes through
// store.Reader (three searches, unit hydration, chunks, entity state, tag counts, stale scopes, page listings),
// graph.Reader (the graph arm), ai.Embedder and ai.Reranker. cmd/memd/wire.go builds one [Engine] with [New]. Besides
// [Engine.Recall] it exposes [Engine.CompileTags] (the tag-filter compiler mental models use for their scope),
// [Engine.SearchPages] and [Engine.ReadPages] (mental-model page search and reads). The pipeline stages (arms,
// fusion, scoring, gates, packing, observation preference, includes) are unexported types tested one by one.
//
// # Usage
//
//	res, err := engine.Recall(ctx, recall.Request{
//		Bank: b, Query: "what does Alice eat?", Budget: domain.BudgetMid, MaxTokens: 2048,
//		Tags: domain.TagFilter{...}, Include: domain.RecallInclude{...},
//	})
//
// [Request.Seq] is for internal callers: a mental-model refresh bounds every read to its snapshot so
// a unit rewritten after the snapshot is not used.
//
// # Flow
//
//  1. Validate (an empty query is domain.ErrInvalidArgument) and resolve the effective config with the
//     request overrides. The resolver reads config in its own short transaction; an unknown or tombstoned
//     bank is domain.ErrNotFound.
//  2. Parse into a parsedQuery: the query cut to [MaxQueryTokens] (500) tokens; the reference time
//     (query_time, else clock.Clock); the per-arm limit from budgetLimit; the tag filter compiled once into
//     a domain.TagPredicate shared by every arm (fuzzy leaves are expanded against the bank's tags, read with
//     Reader.TagCounts, by trigram similarity >= recall.fuzzy_tag_min_similarity); the time window (an
//     explicit window is used as given with no time analysis, else timex derives one, calling TIME_PARSE
//     only for a cue its rules cannot resolve); and the query embedding.
//  3. Retrieve: the enabled arms run concurrently (errgroup), each its own query with no shared snapshot:
//     semantic (HNSW cosine, floor min_scores.semantic, default 0.3, an explicit 0 disables it), keyword
//     (BM25, floor min_scores.keyword when set), graph (its own top 20 semantic seeds at cosine >= 0.3, then
//     graph.Reader expansion over semantic and causal links, never temporal, up to graph_max_hops, re-checked
//     against the store) and temporal (units whose occurrence overlaps the window, spread over temporal_buckets,
//     dropping those with cosine < 0.1 to the query; skipped when there is no window). Any arm error fails the call.
//  4. Fuse by reciprocal-rank fusion: score = sum over arms of 1 / (k + rank), k = recall.rrf_k.
//  5. Keep the top reranker_max_candidates. Only when the pool exceeds that cap do strategy_boosts decide the
//     cut, in rank space (a boost w makes the arm contribute 1 / (k + rank / w)); the stored RRF score stays
//     unboosted.
//  6. Hydrate the kept ids with Reader.GetUnits, dropping units that are gone or whose seq is outside
//     [Request.Seq].
//  7. Score: with recall.rerank on and a reranker wired, the cross-encoder score (RERANK slot) of the text
//     "[Date: ...] context: text" is the base; otherwise the base runs from 1.0 down to 0.1 by unboosted RRF
//     rank. The base is multiplied by three boosts, each 1 + alpha * (signal - 0.5): recency (recency_alpha,
//     linear decay over recency_horizon_days), temporal (temporal_alpha, proximity to the window's middle)
//     and proof (proof_alpha, the log of an observation's proof count). Ties break by RRF, then id.
//  8. Gate with the result floors (min_scores.reranker for reranked candidates, min_scores.final).
//  9. Pack into max_tokens: walk the ranking and keep each unit whose text still fits, skipping one that
//     does not. With prefer_observations, observations within the first 2 x limit candidates first drop the
//     raw facts they cite, and the freed budget backfills from lower-ranked candidates.
//  10. Enrich, each within its own budget: include.chunks (chunks_max_tokens, the chunk that crosses the
//     budget is cut and marked truncated), include.source_facts (facts cited by returned observations,
//     bounded by Seq; one that does not fit is skipped and SourceFactsTruncated is set) and include.entities.
//  11. Mark returned observations Stale when their scope still has unconsolidated facts
//     (Reader.StaleScopes), and fill [Trace] (per-arm counts, timings and top ids, fused, reranked
//     and packed counts, tokens, window) when trace is set.
//
// A recall that finds nothing is not an error. A failing arm, embedder or reranker fails the call (the
// server maps domain.ErrUnavailable to UNAVAILABLE). Recall never writes, so it must not be called inside an
// open DB.InTx.
//
// # Config knobs and defaults
//
// From the effective RecallConfig (the server level sets the starred ones; recall applies the same values
// when unset): budget_function* fixed, budget_low / mid / high* 100 / 300 / 1000 (adaptive: scaled by
// max_tokens / 4096 and clamped to budget_min*..budget_max*, 20..2000), reranker_max_candidates* 300,
// rerank* true (the server level turns it off when no RERANK model is bound), rrf_k* 60, recency_alpha* 0.2,
// temporal_alpha* 0.2, proof_alpha* 0.1, recency_horizon_days 365, graph_max_hops 1 (clamped to 1..2),
// temporal_buckets 4, fuzzy_tag_min_similarity 0.45, strategy_boosts none, enabled_strategies all four.
// Request-level: max_tokens 0 = [DefaultMaxTokens] (4096); include budgets [DefaultChunksMaxTokens] (8192)
// and [DefaultSourceFactsMaxTokens] (4096).
//
// # Tests
//
// Unit tests cover each stage (fusion and boosted trimming, budgets, packing, observation preference,
// includes, score boosts, gates, tag compilation and fuzzy expansion, trigram similarity). Pipeline tests in
// engine_test.go run on a pgtest database with fake.Embedder, fake.Reranker and fake.LLM: arms and trace, the
// default semantic floor, rerank on and off, the seq snapshot, stale observations, no TIME_PARSE call
// without a cue or with an explicit window, a failing arm, a golden fusion order, and an unknown bank. pages_test.go
// covers page search and reads.
// SPEC "Recall pipeline", "Engines" (recall) and "gRPC surface" (Request).
package recall
