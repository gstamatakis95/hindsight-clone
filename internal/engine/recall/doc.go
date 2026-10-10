// Package recall answers the Recall RPC and is the retrieval core reused by reflect, consolidation and
// mental-model refreshes, so it is built first and never imports those engines.
//
// A request is parsed into a ParsedQuery (query embedding, time window from timex, compiled tag
// filter, per-arm limit from the budget), then four Retrievers run against port.Reader: semantic
// (HNSW cosine), keyword (BM25), graph (link expansion from seeds) and temporal (occurrence overlap).
// A Fuser merges their ranked lists with reciprocal-rank fusion; a Scorer applies the cross-encoder
// (RERANK slot) and recency, temporal and proof boosts; ScoreGate applies min_scores; an optional
// ObservationPreference swaps raw facts for the observations covering them; a Packer fits the result
// into max_tokens; and an Includer attaches chunks, source facts and entity state within their own
// budgets. Recall is read-only and stateless: it never writes and never opens a transaction.
package recall
