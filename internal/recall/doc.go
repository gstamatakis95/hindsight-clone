// Package recall is memd's retrieval core: [Engine] answers the Recall RPC and serves reflect (its recall,
// search_observations and page tools) and the mental-model refreshes, so it never imports those packages.
//
// # Where it sits
//
// recall is a library: it imports domain, the store and ai boundaries, config and timex, and never a service, an
// adapter, gen, pgx, net/http, the S3 SDK or Datadog (depguard). Retrieval is split into arms: each [Arm] is one
// retrieval strategy behind a strict interface, and the engine knows arms only by name. The built-in arms (vector,
// text, graph and temporal) live in internal/recall/arms, one file each, and read store.Search and graph.Reader;
// the engine itself never imports them (depguard). The engine reads through store.Reader for unit hydration,
// chunks, entity state, tag counts, stale scopes and page listings, and calls ai.Embedder and ai.Reranker.
// cmd/memd/wire_services.go builds one [Engine] with [New], registering the arms through [Deps.Arms]. Besides
// [Engine.Recall] it exposes [Engine.Arms] (ListRecallArms), [Engine.CheckArms] (reflect's request check),
// [Engine.CompileTags] (the tag-filter compiler mental models use for their scope), [Engine.SearchMentalModels] and
// [Engine.ReadPages] (mental-model page search and reads). The pipeline stages (fusion, scoring, score floors,
// packing, observation preference, includes) are unexported types and functions tested one by one.
//
// # Usage
//
//	res, err := engine.Recall(ctx, recall.Request{
//		Bank: b, Query: "what does Alice eat?", Budget: domain.BudgetMid, MaxTokens: 2048,
//		Arms: []string{"graph", "vector"}, // empty = recall.arms, else every registered arm
//		Tags: domain.TagFilter{...}, Include: domain.RecallInclude{...},
//	})
//
// [Request.Seq] is for internal callers: a mental-model refresh bounds every read to its snapshot so
// a unit rewritten after the snapshot is not used.
//
// # Flow
//
//  1. Validate: an empty query, an unregistered or repeated name in [Request.Arms], or an unregistered name in
//     min_scores.arms is domain.ErrInvalidArgument. Resolve the effective config with the request overrides. The
//     resolver reads config in its own short transaction; an unknown or tombstoned bank is domain.ErrNotFound.
//  2. Parse into a [Query]: the query cut to [MaxQueryTokens] (500) tokens; the reference time (query_time, else
//     clock.Clock); the per-arm limit from budgetLimit; the tag filter compiled once into a domain.TagPredicate
//     shared by every arm (fuzzy leaves are expanded against the bank's tags, read with Reader.TagCounts, by
//     trigram similarity >= recall.fuzzy_tag_min_similarity); the time window (an explicit window is used as
//     given with no time analysis, else timex derives one, calling TIME_PARSE only for a cue its rules cannot
//     resolve); and the query embedding.
//  3. Select the arms: the request's arms when it names any, else the effective recall.arms, else every
//     registered arm; they run in registration order, which is also the fusion order.
//  4. Retrieve: the selected arms run concurrently (errgroup), each its own query with no shared snapshot, each
//     reading its own settings with [Query.ArmSettings] (its min_scores.arms entry and the recall knobs it owns).
//     An arm that returns [ErrSkip] (temporal without a window) is left out; any other arm error fails the call.
//     Each arm's hits are cut to the per-arm limit and de-duplicated, keeping their rank and raw score.
//  5. Fuse by reciprocal-rank fusion: score = sum over arms of 1 / (k + rank), k = recall.rrf_k.
//  6. Keep the top reranker_max_candidates. Only when the pool exceeds that cap do arm_boosts decide the cut, in
//     rank space (a boost w makes the arm contribute 1 / (k + rank / w)); the stored RRF score stays unboosted.
//  7. Hydrate the kept ids with Reader.GetUnits and re-check every hit: only live units of the request's bank that
//     pass its fact types, tag predicate and seq bound survive. This is the strict boundary: whatever an arm
//     returns, recall never leaks another bank's, an archived or a filtered-out unit. The built-in arms filter
//     natively, so for them the re-check drops only units changed since retrieval.
//  8. Score: with recall.rerank on and a reranker wired, the cross-encoder score (RERANK slot) of the text
//     "[Date: ...] context: text" is the base; otherwise the base runs from 1.0 down to 0.1 by unboosted RRF
//     rank. The base is multiplied by three boosts, each 1 + alpha * (signal - 0.5): recency (recency_alpha,
//     linear decay over recency_horizon_days), temporal (temporal_alpha, proximity to the window's middle, for
//     hits of a [WindowArm] such as temporal) and proof (proof_alpha, the log of an observation's proof count).
//     Ties break by RRF, then id.
//  9. Gate with the result floors (min_scores.reranker for reranked candidates, min_scores.final).
//  10. Pack into max_tokens: walk the ranking and keep each unit whose text still fits, skipping one that
//     does not. With prefer_observations, observations within the first 2 x limit candidates first drop the
//     raw facts they cite, and the freed budget backfills from lower-ranked candidates.
//  11. Enrich, each within its own budget: include.chunks (chunks_max_tokens, the chunk that crosses the
//     budget is cut and marked truncated), include.source_facts (facts cited by returned observations,
//     bounded by Seq; one that does not fit is skipped and SourceFactsTruncated is set) and include.entities.
//  12. Mark returned observations Stale when their scope still has unconsolidated facts (Reader.StaleScopes),
//     and fill [Trace] (per-arm counts, timings and top ids by arm name, fused, reranked and packed counts,
//     tokens, window) when trace is set. [Scores.Arms] carries each returned unit's raw score per arm.
//
// A recall that finds nothing is not an error. A failing arm, embedder or reranker fails the call (the
// server maps domain.ErrUnavailable to UNAVAILABLE). Recall never writes, so it must not be called inside an
// open DB.InTx.
//
// # Adding a recall arm
//
//  1. Implement [Arm] in its own file under internal/recall/arms (or its own package): Name (unique, the name
//     clients use), Description (one line, listed by ListRecallArms) and Retrieve, which returns at most
//     Query.Limit hits of Query.Bank, best first, honours ctx, never writes, and returns [ErrSkip] when it does not
//     apply. Read the arm's own settings with [Query.ArmSettings]. Implement [WindowArm] only if its hits are
//     time-window matches that deserve the temporal boost.
//  2. Add a constructor that takes only the boundary the arm reads (store.Search, graph.Reader, ai.Embedder, ...).
//     Arms never import the engine's internals or one another.
//  3. Register it in recallArms in cmd/memd/wire_services.go. Its position is its fusion order.
//  4. Clients then select it by name (RecallRequest.arms, ReflectRequest.arms), configure it (recall.arms,
//     recall.arm_boosts, min_scores.arms; the server validates every name against the registry) and see it in
//     ListRecallArms, traces and Scores.arms. Nothing in the engine changes: never special-case an arm there.
//  5. Run the contract suite internal/recall/armtest from the arm's tests on pgtest, as arms_test.go does.
//
// # Config knobs and defaults
//
// From the effective RecallConfig (the server level sets the starred ones; recall applies the same values
// when unset): budget_function* fixed, budget_low / mid / high* 100 / 300 / 1000 (adaptive: scaled by
// max_tokens / 4096 and clamped to budget_min*..budget_max*, 20..2000), reranker_max_candidates* 300,
// rerank* true (the server level turns it off when no RERANK model is bound), rrf_k* 60, recency_alpha* 0.2,
// temporal_alpha* 0.2, proof_alpha* 0.1, recency_horizon_days 365, fuzzy_tag_min_similarity 0.45, arm_boosts
// none, arms empty (every registered arm). The built-in arms own graph_max_hops (1, clamped to 1..2) and
// temporal_buckets (4). Request-level: max_tokens 0 = [DefaultMaxTokens] (4096); include budgets
// [DefaultChunksMaxTokens] (8192) and [DefaultSourceFactsMaxTokens] (4096).
//
// # Tests
//
// Unit tests cover each stage (fusion and boosted trimming, budgets, packing, observation preference, includes,
// score boosts, gates, tag compilation and fuzzy expansion, trigram similarity). Pipeline tests run on a pgtest
// database with stub arms and fake.Embedder, fake.Reranker and fake.LLM: an extra arm registered, selected, boosted
// and re-checked (arm_test.go), arm selection and name validation, rerank on and off, the seq snapshot, stale
// observations, a failing arm, a golden fusion order and an unknown bank. internal/recall/arms tests the built-in
// arms through the engine (tag groups, floors, trace and selection, time windows) and runs armtest for each.
// mentalmodel_search_test.go covers mental-model search and reads.
// Design: docs/design/recall.md.
package recall
