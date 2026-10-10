// Package recall answers the Recall RPC and is the retrieval core reused by reflect, consolidation and
// mental-model refreshes, so it is built first and never imports those engines.
//
// Recall resolves the effective config (an unknown bank is NOT_FOUND), then parses the request into a
// ParsedQuery: the query embedding, the time window (explicit, else from timex, which calls TIME_PARSE only
// for a cue its rules can't resolve), the tag filter compiled once into a port.SQLPredicate (fuzzy leaves
// resolved by trigram similarity) and the per-arm limit from BudgetLimit. The enabled Retrievers run
// concurrently against port.Reader: semantic (HNSW cosine, min_scores.semantic, default 0.3), keyword (BM25,
// min_scores.keyword), graph (semantic and causal links from its own semantic seeds) and temporal
// (occurrence overlap; skipped without a window). The Fuser merges their lists by reciprocal-rank fusion
// (k = 60). The top reranker_max_candidates (strategy boosts decide that cut, in rank space) are hydrated
// and handed to the Scorer, which takes the
// cross-encoder score (RERANK slot) as the base, or an RRF-rank base when rerank is off, and multiplies in the
// recency, temporal and proof boosts. The ScoreGate applies the result floors; the Packer (or, with
// prefer_observations, the ObservationPreference) fits the result into max_tokens, skipping items that don't
// fit; and the Includer attaches chunks, source facts and entity state within their own budgets. Recall is
// read-only: it never writes, and it reads config in a short transaction of its own.
//
// Reads port.Reader doesn't have yet (bank tags, chunks, entity state) are consumed through the optional
// Lookups interface (CCR WP-09-1).
package recall
